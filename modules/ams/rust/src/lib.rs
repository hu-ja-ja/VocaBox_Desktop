// The implementation of package ams in Rust: VocaBox's player on Windows'
// media stack. AdaptiveMediaSource fetches the signed HLS playlists of
// niconico DOMAND and MediaPlayer plays them, on a thread of its own: the Go
// side calls the methods from the UI thread, so nothing here may block it.
// See 要件/再生バックエンド調査-決定.md for why the app uses this stack.
mod mygo;

use std::sync::atomic::{AtomicBool, AtomicI64, AtomicU64, Ordering};
use std::sync::mpsc::{sync_channel, Receiver, RecvTimeoutError, Sender};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use mygo::*;
use windows::core::{Error as WinError, IInspectable, Ref, HRESULT, HSTRING};
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
use windows::Win32::System::Com::{CoInitializeEx, COINIT_MULTITHREADED};
use windows_future::{AsyncStatus, IAsyncOperation};

// How often the engine looks at a manifest download on its way, and refreshes
// the position the app reads.
const POLL: Duration = Duration::from_millis(20);

// Subscribers is the app's event callback per subscription.
type Subscribers = Arc<Mutex<Vec<(u64, Arc<dyn Fn(Event) + Send + Sync>)>>>;

pub fn make_player() -> Result<Arc<dyn Player>, Error> {
    let (ready_tx, ready_rx) = sync_channel::<Result<(), Error>>(1);
    let (tx, rx) = std::sync::mpsc::channel::<Command>();
    let position = Arc::new(AtomicU64::new(0));
    let generation = Arc::new(AtomicI64::new(0));
    let subscribers: Subscribers = Arc::new(Mutex::new(Vec::new()));
    let (worker_position, worker_generation, worker_subscribers) =
        (position.clone(), generation.clone(), subscribers.clone());
    std::thread::Builder::new()
        .name("ams".into())
        .spawn(move || {
            run(
                rx,
                ready_tx,
                worker_position,
                worker_generation,
                worker_subscribers,
            )
        })
        .map_err(|err| Error::other(format!("ams: starting the player thread: {err}")))?;
    match ready_rx.recv() {
        Ok(Ok(())) => {}
        Ok(Err(err)) => return Err(err),
        Err(_) => return Err(Error::other("ams: the player thread stopped")),
    }
    Ok(Arc::new(NativePlayer {
        tx: Mutex::new(tx),
        position,
        subscribers,
        closed: AtomicBool::new(false),
        next_subscriber: AtomicU64::new(1),
    }))
}

struct NativePlayer {
    tx: Mutex<Sender<Command>>,
    // position is the last position the media stack reported, in bits of an
    // f64: the app reads it at its own rate instead of asking the media stack
    // on the UI thread.
    position: Arc<AtomicU64>,
    subscribers: Subscribers,
    closed: AtomicBool,
    next_subscriber: AtomicU64,
}

impl NativePlayer {
    fn send(&self, command: Command) -> Result<(), Error> {
        if self.closed.load(Ordering::SeqCst) {
            return Err(Error::other("ams: the player is closed"));
        }
        self.tx
            .lock()
            .map_err(|_| Error::other("ams: the player is closed"))?
            .send(command)
            .map_err(|_| Error::other("ams: the player stopped"))
    }
}

impl Player for NativePlayer {
    fn play(&self, load: Load) -> Result<(), Error> {
        self.send(Command::Play(load))
    }

    fn pause(&self) -> Result<(), Error> {
        self.send(Command::Pause)
    }

    fn resume(&self) -> Result<(), Error> {
        self.send(Command::Resume)
    }

    fn seek(&self, ms: f64) -> Result<(), Error> {
        self.send(Command::Seek(ms))
    }

    fn set_volume(&self, v: f64) -> Result<(), Error> {
        self.send(Command::Volume(v))
    }

    fn stop(&self) -> Result<(), Error> {
        self.send(Command::Stop)
    }

    fn position(&self) -> f64 {
        f64::from_bits(self.position.load(Ordering::Relaxed))
    }

    fn on_event(&self, f: Arc<dyn Fn(Event) + Send + Sync>) -> Box<dyn Closer> {
        let id = self.next_subscriber.fetch_add(1, Ordering::Relaxed);
        if let Ok(mut subscribers) = self.subscribers.lock() {
            subscribers.push((id, f));
        }
        let subscribers = self.subscribers.clone();
        closer(move || {
            if let Ok(mut subscribers) = subscribers.lock() {
                subscribers.retain(|(subscriber, _)| *subscriber != id);
            }
        })
    }

    fn close(&self) -> Result<(), Error> {
        if self.closed.swap(true, Ordering::SeqCst) {
            return Ok(());
        }
        if let Ok(tx) = self.tx.lock() {
            let _ = tx.send(Command::Close);
        }
        self.position.store(0.0f64.to_bits(), Ordering::Relaxed);
        Ok(())
    }
}

enum Command {
    Play(Load),
    Pause,
    Resume,
    Seek(f64),
    Volume(f64),
    Stop,
    Close,
}

// run is the player's thread: it makes the media stack, hears the app's
// commands, and keeps the position the app reads up to date.
fn run(
    rx: Receiver<Command>,
    ready: std::sync::mpsc::SyncSender<Result<(), Error>>,
    position: Arc<AtomicU64>,
    generation: Arc<AtomicI64>,
    subscribers: Subscribers,
) {
    if let Err(err) = unsafe { CoInitializeEx(None, COINIT_MULTITHREADED) }.ok() {
        let _ = ready.send(Err(Error::other(format!("ams: COM initialization: {err}"))));
        return;
    }
    let player = match MediaPlayer::new() {
        Ok(player) => player,
        Err(err) => {
            let _ = ready.send(Err(Error::other(format!("ams: MediaPlayer: {err}"))));
            return;
        }
    };
    let filter = match HttpBaseProtocolFilter::new() {
        Ok(filter) => filter,
        Err(err) => {
            let _ = ready.send(Err(Error::other(format!("ams: HTTP filter: {err}"))));
            return;
        }
    };
    // NoCookies: the app hands the player its cookie header per track, so
    // nothing of the app's session lands in the system cookie store the
    // filter's jar writes to.
    let _ = filter.SetCookieUsageBehavior(HttpCookieUsageBehavior::NoCookies);
    let http = match HttpClient::Create(&filter) {
        Ok(http) => http,
        Err(err) => {
            let _ = ready.send(Err(Error::other(format!("ams: HTTP client: {err}"))));
            return;
        }
    };

    let opened = {
        let generation = generation.clone();
        let subscribers = subscribers.clone();
        TypedEventHandler::new(move |sender: Ref<MediaPlayer>, _: Ref<IInspectable>| {
            let duration_ms = sender
                .ok()
                .and_then(|player| player.PlaybackSession())
                .and_then(|session| session.NaturalDuration())
                .ok()
                .and_then(|duration| natural_duration_ms(duration.Duration));
            emit(
                &subscribers,
                Event {
                    event: "opened".into(),
                    duration_ms: duration_ms.unwrap_or(0.0),
                    generation: generation.load(Ordering::SeqCst),
                    ..Default::default()
                },
            );
            Ok(())
        })
    };
    let ended = {
        let generation = generation.clone();
        let subscribers = subscribers.clone();
        TypedEventHandler::new(move |_: Ref<MediaPlayer>, _: Ref<IInspectable>| {
            emit(
                &subscribers,
                Event {
                    event: "ended".into(),
                    generation: generation.load(Ordering::SeqCst),
                    ..Default::default()
                },
            );
            Ok(())
        })
    };
    let failed = {
        let generation = generation.clone();
        let subscribers = subscribers.clone();
        TypedEventHandler::new(
            move |_: Ref<MediaPlayer>, args: Ref<MediaPlayerFailedEventArgs>| {
                let args = args.as_ref();
                let code = args
                    .and_then(|args| args.Error().ok())
                    .map_or_else(|| "Unknown".to_string(), player_error_name);
                let ext = args
                    .and_then(|args| args.ExtendedErrorCode().ok())
                    .map_or(0, |hr| hr.0 as i64);
                let msg = args
                    .and_then(|args| args.ErrorMessage().ok())
                    .map_or_else(String::new, |msg| msg.to_string());
                emit(
                    &subscribers,
                    Event {
                        event: "failed".into(),
                        code,
                        ext,
                        msg,
                        generation: generation.load(Ordering::SeqCst),
                        ..Default::default()
                    },
                );
                Ok(())
            },
        )
    };
    let _ = player.MediaOpened(&opened);
    let _ = player.MediaEnded(&ended);
    let _ = player.MediaFailed(&failed);

    let mut engine = Engine {
        player,
        http,
        current: None,
        source: None,
        pending: None,
        generation: 0,
        want_play: false,
        position,
        generation_name: generation,
        subscribers,
    };
    let _ = ready.send(Ok(()));
    loop {
        engine.poll();
        engine.tick();
        match rx.recv_timeout(POLL) {
            Ok(Command::Close) => {
                engine.stop();
                break;
            }
            Ok(command) => engine.handle(command),
            Err(RecvTimeoutError::Timeout) => {}
            Err(RecvTimeoutError::Disconnected) => break,
        }
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
    position: Arc<AtomicU64>,
    // generation_name names the load the current source belongs to, on every
    // event the media stack reports: the app drops the events of a replaced
    // track by it.
    generation_name: Arc<AtomicI64>,
    subscribers: Subscribers,
}

struct Pending {
    op: IAsyncOperation<AdaptiveMediaSourceCreationResult>,
    generation: i64,
    name: i64,
}

impl Engine {
    fn handle(&mut self, command: Command) {
        match command {
            Command::Play(load) => self.play(load),
            Command::Pause => {
                self.want_play = false;
                let _ = self.player.Pause();
            }
            Command::Resume => {
                self.want_play = true;
                let _ = self.player.Play();
            }
            Command::Seek(ms) => {
                if self.source.is_some() {
                    if let Ok(session) = self.player.PlaybackSession() {
                        let _ = session.SetPosition(millis(ms.max(0.0)));
                    }
                }
            }
            Command::Volume(value) => {
                let _ = self.player.SetVolume(value.clamp(0.0, 1.0));
            }
            Command::Stop => self.stop(),
            Command::Close => {}
        }
    }

    // play installs the playlist at load.url and starts it unless the load
    // says it is paused. Cookie and UA are the ones the app fetched the
    // playlist with; the delivery hosts need the domand_bid cookie. The
    // load's generation stamps the events this source brings. A load naming
    // a file plays the app's saved copy instead: the media stack decodes it
    // as it decodes a stream, only the way the source is made differs.
    fn play(&mut self, load: Load) {
        if !load.file.is_empty() {
            self.play_file(load);
            return;
        }
        if load.url.is_empty() {
            // A play with no source is the app's bug: reporting it keeps the
            // app from waiting on a source that never comes.
            self.fail(self.generation, load.generation, "NoSource", 0, "");
            return;
        }
        self.stop();
        self.generation += 1;
        self.want_play = load.start;
        let op = match self.start(&load) {
            Ok(op) => op,
            Err(err) => {
                let generation = self.generation;
                self.fail(
                    generation,
                    load.generation,
                    "Exception",
                    err.code().0 as i64,
                    &err.message(),
                );
                return;
            }
        };
        self.pending = Some(Pending {
            op,
            generation: self.generation,
            name: load.generation,
        });
    }

    // play_file opens one saved track from disk. The file URI is the app's own
    // path, so no cookie or user agent rides along, and nothing is polled: the
    // source is made at once.
    fn play_file(&mut self, load: Load) {
        self.stop();
        self.generation += 1;
        self.want_play = load.start;
        let source = Uri::CreateUri(&HSTRING::from(load.file.as_str()))
            .and_then(|uri| MediaSource::CreateFromUri(&uri));
        let source = match source {
            Ok(source) => source,
            Err(err) => {
                let generation = self.generation;
                self.fail(
                    generation,
                    load.generation,
                    "SourceFailed",
                    err.code().0 as i64,
                    &err.message(),
                );
                return;
            }
        };
        self.generation_name
            .store(load.generation, Ordering::SeqCst);
        let _ = self.player.SetSource(&source);
        self.source = Some(source);
        if self.want_play {
            let _ = self.player.Play();
        }
    }

    // start hands the playlist to the media stack. The download it begins is
    // polled for rather than waited on, so the commands that arrive meanwhile
    // still get an answer.
    fn start(
        &self,
        load: &Load,
    ) -> windows::core::Result<IAsyncOperation<AdaptiveMediaSourceCreationResult>> {
        self.apply_cookies(&load.cookie);
        self.apply_user_agent(&load.user_agent);
        let uri = Uri::CreateUri(&HSTRING::from(load.url.as_str()))?;
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
                    pending.name,
                    "Exception",
                    err.code().0 as i64,
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
                pending.name,
                &creation_status_name(status),
                ext.0 as i64,
                &hresult_message(ext),
            );
            return;
        }
        let current = match result.MediaSource() {
            Ok(current) => current,
            Err(err) => {
                self.fail(
                    pending.generation,
                    pending.name,
                    "SourceFailed",
                    err.code().0 as i64,
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
                    pending.name,
                    "SourceFailed",
                    err.code().0 as i64,
                    &err.message(),
                );
                return;
            }
        };
        self.generation_name.store(pending.name, Ordering::SeqCst);
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

    // tick refreshes the position the app reads. The media stack is asked on
    // this thread only.
    fn tick(&mut self) {
        let ms = if self.source.is_none() {
            0.0
        } else {
            self.player
                .PlaybackSession()
                .and_then(|session| session.Position())
                .map_or(0.0, |position| position.Duration as f64 / 10_000.0)
        };
        self.position.store(ms.to_bits(), Ordering::Relaxed);
    }

    fn stop(&mut self) {
        self.generation += 1;
        self.generation_name.store(0, Ordering::SeqCst);
        self.position.store(0.0f64.to_bits(), Ordering::Relaxed);
        self.want_play = false;
        self.pending = None;
        let _ = self.player.Pause();
        let _ = self.player.SetSource(None::<&IMediaPlaybackSource>);
        self.source = None;
        self.current = None;
    }

    fn fail(&self, generation: i64, name: i64, code: &str, ext: i64, msg: &str) {
        if generation != self.generation {
            return;
        }
        emit(
            &self.subscribers,
            Event {
                event: "failed".into(),
                code: code.to_string(),
                ext,
                msg: msg.to_string(),
                generation: name,
                ..Default::default()
            },
        );
    }

    // apply_cookies puts the app's cookie header on every request of the
    // media stack, adaptive source included. The cookie stays in the app's
    // own jar; the player holds it only for the life of the playback.
    fn apply_cookies(&self, cookie: &str) {
        let Ok(jar) = self
            .http
            .DefaultRequestHeaders()
            .and_then(|headers| headers.Cookie())
        else {
            return;
        };
        let _ = jar.Clear();
        for pair in cookie.split(';').map(str::trim) {
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

    fn apply_user_agent(&self, ua: &str) {
        if ua.is_empty() {
            return;
        }
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

// emit calls every subscriber of the player. The list is copied first: a
// callback may unsubscribe from inside its own call.
fn emit(subscribers: &Subscribers, event: Event) {
    let listeners: Vec<_> = match subscribers.lock() {
        Ok(subscribers) => subscribers.iter().map(|(_, f)| f.clone()).collect(),
        Err(_) => return,
    };
    for f in listeners {
        f(event.clone());
    }
}

fn millis(ms: f64) -> TimeSpan {
    TimeSpan {
        Duration: (ms * 10_000.0) as i64,
    }
}

// natural_duration_ms is NaturalDuration as the event's duration, none when
// it names no length: a saved file is a fragmented MP4 with no movie
// duration, and the media stack answers with TimeSpan's maximum rather than a
// length. The app's own metadata is the truth there.
fn natural_duration_ms(ticks: i64) -> Option<f64> {
    (ticks > 0 && ticks != i64::MAX).then(|| ticks as f64 / 10_000.0)
}

// hresult_message is the system's text for a failure code, "" when there is
// none to report.
fn hresult_message(hr: HRESULT) -> String {
    if hr.0 == 0 {
        return String::new();
    }
    WinError::from_hresult(hr).message().to_string()
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn natural_duration_reports_only_a_length() {
        assert_eq!(natural_duration_ms(99_150 * 10_000), Some(99_150.0));
        assert_eq!(natural_duration_ms(0), None);
        assert_eq!(natural_duration_ms(-1), None);
        assert_eq!(natural_duration_ms(i64::MAX), None);
    }
}
