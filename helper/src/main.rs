// AmsHelper is VocaBox's resident playback helper: it plays one niconico
// DOMAND HLS audio playlist at a time through the OS media stack
// (AdaptiveMediaSource + MediaPlayer inside Windows) and is driven by JSON
// lines over stdin/stdout. It runs as a separate process so the app's Go code
// needs no WinRT bindings and a media-stack crash cannot take the app down.
// The protocol is documented in README.md beside this file.
use std::io::{BufRead, Write};
use std::sync::atomic::{AtomicI32, Ordering};
use std::sync::mpsc::{RecvTimeoutError, channel};
use std::time::Duration;

use serde::{Deserialize, Serialize};
use windows::Foundation::{TimeSpan, TypedEventHandler, Uri};
use windows::Media::Core::MediaSource;
use windows::Media::Playback::{
    IMediaPlaybackSource, MediaPlayer, MediaPlayerError, MediaPlayerFailedEventArgs,
};
use windows::Media::Streaming::Adaptive::{
    AdaptiveMediaSource, AdaptiveMediaSourceCreationResult, AdaptiveMediaSourceCreationStatus,
};
use windows::Web::Http::Filters::{HttpBaseProtocolFilter, HttpCookieUsageBehavior};
use windows::Web::Http::Headers::HttpCookiePairHeaderValue;
use windows::Web::Http::HttpClient;
use windows::Win32::System::Com::{COINIT_MULTITHREADED, CoInitializeEx};
use windows::core::*;
use windows_future::{AsyncStatus, IAsyncOperation};

// How often a manifest download still on its way is looked at.
const POLL: Duration = Duration::from_millis(20);

// gen is the app's name of the track load the current source belongs to,
// echoed on every event so the app can drop the events of a replaced track.
static GEN: AtomicI32 = AtomicI32::new(0);

fn main() {
    unsafe {
        if CoInitializeEx(None, COINIT_MULTITHREADED).is_err() {
            eprintln!("AmsHelper: COM initialization failed");
            return;
        }
    }
    let player = MediaPlayer::new().expect("MediaPlayer");
    let filter = HttpBaseProtocolFilter::new().expect("HTTP filter");
    // NoCookies: the app hands the helper its cookie header per track, so
    // nothing of the app's session lands in the system cookie store the
    // filter's jar writes to.
    let _ = filter.SetCookieUsageBehavior(HttpCookieUsageBehavior::NoCookies);
    let http = HttpClient::Create(&filter).expect("HTTP client");

    let _ = player.MediaOpened(&TypedEventHandler::new(
        |sender: Ref<MediaPlayer>, _: Ref<IInspectable>| {
            let ms = sender
                .ok()
                .and_then(|player| player.PlaybackSession())
                .and_then(|session| session.NaturalDuration())
                .map_or(0.0, |duration| duration.Duration as f64 / 10_000.0);
            emit(Event {
                ev: "opened",
                duration_ms: Some(ms),
                r#gen: GEN.load(Ordering::SeqCst),
                ..Default::default()
            });
            Ok(())
        },
    ));
    let _ = player.MediaEnded(&TypedEventHandler::new(
        |_: Ref<MediaPlayer>, _: Ref<IInspectable>| {
            emit(Event {
                ev: "ended",
                r#gen: GEN.load(Ordering::SeqCst),
                ..Default::default()
            });
            Ok(())
        },
    ));
    let _ = player.MediaFailed(&TypedEventHandler::new(
        |_: Ref<MediaPlayer>, args: Ref<MediaPlayerFailedEventArgs>| {
            let args = args.as_ref();
            let code = args
                .and_then(|args| args.Error().ok())
                .map_or_else(|| "Unknown".to_string(), player_error_name);
            let ext = args
                .and_then(|args| args.ExtendedErrorCode().ok())
                .map_or(0, |hr| hr.0);
            let msg = args
                .and_then(|args| args.ErrorMessage().ok())
                .map_or_else(String::new, |msg| msg.to_string());
            emit(Event {
                ev: "failed",
                code: Some(code),
                ext: Some(ext),
                msg: Some(msg),
                r#gen: GEN.load(Ordering::SeqCst),
                ..Default::default()
            });
            Ok(())
        },
    ));

    // stdin is read on its own thread: the command loop has to stay free to
    // look at the manifest download it started.
    let (tx, rx) = channel::<Command>();
    std::thread::spawn(move || {
        for line in std::io::stdin().lock().lines() {
            let Ok(line) = line else { break };
            let Ok(command) = serde_json::from_str::<Command>(&line) else {
                // A malformed line is the app's bug, not a reason to stop.
                continue;
            };
            if command.cmd == "quit" || tx.send(command).is_err() {
                break;
            }
        }
    });

    // Engine owns the player and the source it was handed. Commands reach it
    // from this one loop; the media callbacks run on the media stack's own
    // threads and only read GEN.
    let mut engine = Engine {
        player,
        http,
        current: None,
        source: None,
        pending: None,
        generation: 0,
        want_play: false,
    };
    loop {
        engine.poll();
        let command = if engine.pending.is_some() {
            match rx.recv_timeout(POLL) {
                Ok(command) => command,
                Err(RecvTimeoutError::Timeout) => continue,
                Err(RecvTimeoutError::Disconnected) => break,
            }
        } else if let Ok(command) = rx.recv() {
            command
        } else {
            break;
        };
        engine.handle(command);
    }
    engine.stop();
}

struct Engine {
    player: MediaPlayer,
    http: HttpClient,
    current: Option<AdaptiveMediaSource>,
    // source wraps current for the player: setting Source to null detaches
    // it without freeing the media stack's buffers, so a replaced track is
    // dropped explicitly rather than left to the last reference.
    source: Option<MediaSource>,
    // pending is a manifest download still on its way; one that a later
    // command superseded is dropped when it lands.
    pending: Option<Pending>,
    generation: i64,
    want_play: bool,
}

struct Pending {
    op: IAsyncOperation<AdaptiveMediaSourceCreationResult>,
    generation: i64,
    r#gen: i32,
}

impl Engine {
    fn handle(&mut self, command: Command) {
        match command.cmd.as_str() {
            "play" => self.play(&command),
            "pause" => {
                self.want_play = false;
                let _ = self.player.Pause();
            }
            "resume" => {
                self.want_play = true;
                let _ = self.player.Play();
            }
            "seek" => {
                if self.current.is_some()
                    && let Ok(session) = self.player.PlaybackSession()
                {
                    let _ = session.SetPosition(millis(command.ms.unwrap_or(0.0).max(0.0)));
                }
            }
            "volume" => {
                let _ = self
                    .player
                    .SetVolume(command.value.unwrap_or(0.0).clamp(0.0, 1.0));
            }
            "stop" => self.stop(),
            "position" => {
                let ms = if self.current.is_none() {
                    0.0
                } else {
                    self.player
                        .PlaybackSession()
                        .and_then(|session| session.Position())
                        .map_or(0.0, |position| position.Duration as f64 / 10_000.0)
                };
                emit(Event {
                    ev: "position",
                    ms: Some(ms),
                    r#gen: GEN.load(Ordering::SeqCst),
                    ..Default::default()
                });
            }
            _ => {}
        }
    }

    // play installs the playlist at url and starts it unless the command
    // says it is paused. Cookie and UA are the ones the app fetched the
    // playlist with; the delivery hosts need the domand_bid cookie. Gen
    // stamps the events this source brings.
    fn play(&mut self, command: &Command) {
        let Some(url) = command.url.as_deref().filter(|url| !url.is_empty()) else {
            return;
        };
        self.stop();
        self.generation += 1;
        self.want_play = command.play;
        let op = match self.start(command, url) {
            Ok(op) => op,
            Err(err) => {
                let generation = self.generation;
                self.fail(
                    generation,
                    command.r#gen,
                    "Exception",
                    err.code().0,
                    &err.message(),
                );
                return;
            }
        };
        self.pending = Some(Pending {
            op,
            generation: self.generation,
            r#gen: command.r#gen,
        });
    }

    // start hands the playlist to the media stack. The download it begins is
    // polled for rather than waited on, so the commands that arrive meanwhile
    // still get an answer.
    fn start(
        &self,
        command: &Command,
        url: &str,
    ) -> Result<IAsyncOperation<AdaptiveMediaSourceCreationResult>> {
        self.apply_cookies(command.cookie.as_deref());
        self.apply_user_agent(command.ua.as_deref());
        let uri = Uri::CreateUri(&HSTRING::from(url))?;
        AdaptiveMediaSource::CreateFromUriWithDownloaderAsync(&uri, &self.http)
    }

    // poll installs a finished manifest download, or reports its failure.
    fn poll(&mut self) {
        let downloading = self
            .pending
            .as_ref()
            .is_some_and(|pending| matches!(pending.op.Status(), Ok(AsyncStatus::Started)));
        if downloading {
            return;
        }
        let Some(pending) = self.pending.take() else {
            return;
        };
        let result = match pending.op.join() {
            Ok(result) => result,
            Err(err) => {
                self.fail(
                    pending.generation,
                    pending.r#gen,
                    "Exception",
                    err.code().0,
                    &err.message(),
                );
                return;
            }
        };
        if pending.generation != self.generation {
            // The app replaced this load while its manifest was on its way.
            return;
        }
        let status = match result.Status() {
            Ok(status) => status,
            Err(_) => AdaptiveMediaSourceCreationStatus::UnknownFailure,
        };
        if status != AdaptiveMediaSourceCreationStatus::Success {
            let ext = result.ExtendedError().unwrap_or(HRESULT(0));
            self.fail(
                pending.generation,
                pending.r#gen,
                &creation_status_name(status),
                ext.0,
                &hresult_message(ext),
            );
            return;
        }
        let current = match result.MediaSource() {
            Ok(current) => current,
            Err(err) => {
                self.fail(
                    pending.generation,
                    pending.r#gen,
                    "SourceFailed",
                    err.code().0,
                    &err.message(),
                );
                return;
            }
        };
        let source = match MediaSource::CreateFromAdaptiveMediaSource(&current) {
            Ok(source) => source,
            Err(err) => {
                self.fail(
                    pending.generation,
                    pending.r#gen,
                    "SourceFailed",
                    err.code().0,
                    &err.message(),
                );
                return;
            }
        };
        GEN.store(pending.r#gen, Ordering::SeqCst);
        self.current = Some(current);
        self.source = Some(source);
        if let Some(source) = self.source.as_ref() {
            let _ = self.player.SetSource(source);
        }
        // A track installed for a pause, or paused while its manifest
        // downloaded, stays paused: the UI's play state and the app's
        // wantPlaying must agree.
        if self.want_play {
            let _ = self.player.Play();
        }
    }

    fn stop(&mut self) {
        self.generation += 1;
        GEN.store(0, Ordering::SeqCst);
        self.want_play = false;
        self.pending = None;
        let _ = self.player.Pause();
        let _ = self.player.SetSource(None::<&IMediaPlaybackSource>);
        self.source = None;
        self.current = None;
    }

    fn fail(&self, generation: i64, r#gen: i32, code: &str, ext: i32, msg: &str) {
        if generation != self.generation {
            return;
        }
        emit(Event {
            ev: "failed",
            code: Some(code.to_string()),
            ext: Some(ext),
            msg: Some(msg.to_string()),
            r#gen,
            ..Default::default()
        });
    }

    // apply_cookies puts the app's cookie header on every request of the
    // media stack, adaptive source included. The cookie stays in the app's
    // own jar; the helper's process holds it only for the life of the
    // playback.
    fn apply_cookies(&self, cookie: Option<&str>) {
        let Ok(jar) = self
            .http
            .DefaultRequestHeaders()
            .and_then(|headers| headers.Cookie())
        else {
            return;
        };
        let _ = jar.Clear();
        for pair in cookie.unwrap_or_default().split(';').map(str::trim) {
            let Some((name, value)) = pair.split_once('=').filter(|(name, _)| !name.is_empty())
            else {
                continue;
            };
            // One malformed pair must not keep the track from playing.
            if let Ok(entry) = HttpCookiePairHeaderValue::CreateFromNameWithValue(
                &HSTRING::from(name),
                &HSTRING::from(value),
            ) {
                let _ = jar.Append(&entry);
            }
        }
    }

    fn apply_user_agent(&self, ua: Option<&str>) {
        let Some(ua) = ua.filter(|ua| !ua.is_empty()) else {
            return;
        };
        let Ok(agent) = self
            .http
            .DefaultRequestHeaders()
            .and_then(|headers| headers.UserAgent())
        else {
            return;
        };
        // The app's UA is the same for every track, so it is replaced rather
        // than compared first; a UA the parser rejects leaves the requests
        // without one of ours instead of keeping the previous value.
        let _ = agent.Clear();
        let _ = agent.TryParseAdd(&HSTRING::from(ua));
    }
}

fn millis(ms: f64) -> TimeSpan {
    TimeSpan {
        Duration: (ms * 10_000.0) as i64,
    }
}

// hresult_message is the system's text for a failure code, "" when there is
// none to report.
fn hresult_message(hr: HRESULT) -> String {
    if hr.0 == 0 {
        return String::new();
    }
    Error::from_hresult(hr).message().to_string()
}

// player_error_name names the way the C# helper's MediaPlayerError.ToString
// did; a value the projection has no name for prints its number, as that did.
fn player_error_name(error: MediaPlayerError) -> String {
    let name = if error == MediaPlayerError::Unknown {
        "Unknown"
    } else if error == MediaPlayerError::Aborted {
        "Aborted"
    } else if error == MediaPlayerError::NetworkError {
        "NetworkError"
    } else if error == MediaPlayerError::DecodingError {
        "DecodingError"
    } else if error == MediaPlayerError::SourceNotSupported {
        "SourceNotSupported"
    } else {
        return error.0.to_string();
    };
    name.to_string()
}

fn creation_status_name(status: AdaptiveMediaSourceCreationStatus) -> String {
    let name = if status == AdaptiveMediaSourceCreationStatus::Success {
        "Success"
    } else if status == AdaptiveMediaSourceCreationStatus::ManifestDownloadFailure {
        "ManifestDownloadFailure"
    } else if status == AdaptiveMediaSourceCreationStatus::ManifestParseFailure {
        "ManifestParseFailure"
    } else if status == AdaptiveMediaSourceCreationStatus::UnsupportedManifestContentType {
        "UnsupportedManifestContentType"
    } else if status == AdaptiveMediaSourceCreationStatus::UnsupportedManifestVersion {
        "UnsupportedManifestVersion"
    } else if status == AdaptiveMediaSourceCreationStatus::UnsupportedManifestProfile {
        "UnsupportedManifestProfile"
    } else if status == AdaptiveMediaSourceCreationStatus::UnknownFailure {
        "UnknownFailure"
    } else {
        return status.0.to_string();
    };
    name.to_string()
}

// emit writes one event line. The command loop and the media callbacks share
// stdout, so every line goes out whole and flushed, and the app hears it at
// once.
fn emit(event: Event) {
    let Ok(line) = serde_json::to_string(&event) else {
        return;
    };
    let stdout = std::io::stdout();
    let mut out = stdout.lock();
    let _ = writeln!(out, "{line}");
    let _ = out.flush();
}

#[derive(Deserialize)]
struct Command {
    cmd: String,
    #[serde(default)]
    url: Option<String>,
    #[serde(default)]
    cookie: Option<String>,
    #[serde(default)]
    ua: Option<String>,
    #[serde(default)]
    ms: Option<f64>,
    #[serde(default)]
    value: Option<f64>,
    #[serde(default)]
    r#gen: i32,
    #[serde(default)]
    play: bool,
}

// Event is one stdout line. A field is written only when it is set, as the
// C# helper's serializer did.
#[derive(Default, Serialize)]
struct Event {
    ev: &'static str,
    #[serde(rename = "durationMs", skip_serializing_if = "Option::is_none")]
    duration_ms: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    ms: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    ext: Option<i32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    msg: Option<String>,
    r#gen: i32,
}
