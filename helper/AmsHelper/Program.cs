// AmsHelper is VocaBox's resident playback helper: it plays one niconico
// DOMAND HLS audio playlist at a time through the OS media stack
// (AdaptiveMediaSource + MediaPlayer inside Windows) and is driven by JSON
// lines over stdin/stdout. It runs as a separate process so the app's Go code
// needs no WinRT bindings and a media-stack crash cannot take the app down.
// The protocol is documented in README.md beside this file.
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using Windows.Media.Core;
using Windows.Media.Playback;
using Windows.Media.Streaming.Adaptive;
using Windows.Web.Http;
using Windows.Web.Http.Filters;
using Windows.Web.Http.Headers;
// System.Net.Http is in the implicit usings; playback needs the WinRT HTTP
// stack, which is the client AdaptiveMediaSource makes its own requests with.
using HttpClient = Windows.Web.Http.HttpClient;

internal static class Program
{
    // Console output is shared by the command loop and the media callbacks:
    // every line goes out whole and flushed, so the app hears it at once.
    private static readonly object outLock = new();
    private static readonly StreamWriter Out =
        new(Console.OpenStandardOutput(), new UTF8Encoding(false)) { AutoFlush = true };

    private static readonly MediaPlayer Player = new();
    // NoCookies: the app hands the helper its cookie header per track, so
    // nothing of the app's session lands in the system cookie store the
    // filter's jar writes to.
    private static readonly HttpBaseProtocolFilter Filter =
        new() { CookieUsageBehavior = HttpCookieUsageBehavior.NoCookies };
    private static readonly HttpClient Http = new(Filter);

    // current is the adaptive source feeding the player; generation marks the
    // newest play, so a slow manifest download cannot install a stale source
    // over a newer one.
    private static AdaptiveMediaSource? _current;
    private static volatile int _generation;
    // gen is the app's name of the track load the current source belongs to,
    // echoed on every event so the app can drop the events of a replaced
    // track.
    private static volatile int _gen;

    private static int Main()
    {
        Player.MediaOpened += (_, _) => Emit(new HelperEvent
        {
            Ev = "opened",
            DurationMs = Player.PlaybackSession.NaturalDuration.TotalMilliseconds,
            Gen = _gen,
        });
        Player.MediaEnded += (_, _) => Emit(new HelperEvent { Ev = "ended", Gen = _gen });
        Player.MediaFailed += (_, e) => Emit(new HelperEvent
        {
            Ev = "failed",
            Code = e.Error.ToString(),
            Ext = e.ExtendedErrorCode?.HResult ?? 0,
            Msg = e.ErrorMessage,
            Gen = _gen,
        });

        using var input = new StreamReader(Console.OpenStandardInput(), new UTF8Encoding(false));
        while (input.ReadLine() is { } line)
        {
            if (line.Length == 0)
            {
                continue;
            }
            HelperCommand? command = null;
            try
            {
                command = JsonSerializer.Deserialize(line, HelperJsonContext.Default.HelperCommand);
            }
            catch (JsonException)
            {
                // A malformed line is the app's bug, not a reason to stop.
            }
            if (command is null)
            {
                continue;
            }
            if (command.Cmd == "quit")
            {
                break;
            }
            Handle(command);
        }
        Player.Dispose();
        return 0;
    }

    private static void Handle(HelperCommand command)
    {
        switch (command.Cmd)
        {
            case "play":
                Play(command);
                break;
            case "pause":
                Player.Pause();
                break;
            case "resume":
                Player.Play();
                break;
            case "seek":
                if (_current is not null)
                {
                    Player.PlaybackSession.Position = TimeSpan.FromMilliseconds(Math.Max(0, command.Ms));
                }
                break;
            case "volume":
                Player.Volume = Math.Clamp(command.Value, 0, 1);
                break;
            case "stop":
                Stop();
                break;
            case "position":
                Emit(new HelperEvent
                {
                    Ev = "position",
                    Ms = _current is null ? 0 : Player.PlaybackSession.Position.TotalMilliseconds,
                    Gen = _gen,
                });
                break;
        }
    }

    // Play installs the playlist at url and starts it unless the command
    // says it is paused; a play that arrives while an earlier manifest still
    // downloads wins, and the earlier result is dropped. Cookie and UA are
    // the ones the app fetched the playlist with; the delivery hosts need the
    // domand_bid cookie. Gen stamps the events this source brings.
    private static async void Play(HelperCommand command)
    {
        if (command.Url is not { Length: > 0 } url)
        {
            return;
        }
        var generation = ++_generation;
        _gen = command.Gen;
        ApplyCookies(command.Cookie);
        if (command.Ua is { Length: > 0 } ua && Http.DefaultRequestHeaders.UserAgent.ToString() != ua)
        {
            Http.DefaultRequestHeaders.UserAgent.Clear();
            Http.DefaultRequestHeaders.UserAgent.TryParseAdd(ua);
        }
        AdaptiveMediaSourceCreationResult result;
        try
        {
            result = await AdaptiveMediaSource.CreateFromUriAsync(new Uri(url), Http);
        }
        catch (Exception ex)
        {
            Fail(generation, "Exception", ex.HResult, ex.Message);
            return;
        }
        if (generation != _generation)
        {
            result.MediaSource?.Dispose();
            return;
        }
        if (result.Status != AdaptiveMediaSourceCreationStatus.Success)
        {
            Fail(generation, result.Status.ToString(), result.ExtendedError?.HResult ?? 0,
                result.ExtendedError?.Message ?? "");
            return;
        }
        var previous = _current;
        _current = result.MediaSource;
        Player.Source = null;
        previous?.Dispose();
        Player.Source = MediaSource.CreateFromAdaptiveMediaSource(_current);
        // A track installed for a pause stays paused: the UI's play state and
        // the app's wantPlaying must agree.
        if (command.Play)
        {
            Player.Play();
        }
    }

    private static void Stop()
    {
        _generation++;
        Player.Source = null;
        _current?.Dispose();
        _current = null;
    }

    // ApplyCookies puts the app's cookie header on every request of the media
    // stack, adaptive source included. The cookie stays in the app's own jar;
    // the helper's process holds it only for the life of the playback.
    private static void ApplyCookies(string? cookie)
    {
        Http.DefaultRequestHeaders.Cookie.Clear();
        if (cookie is not { Length: > 0 })
        {
            return;
        }
        foreach (var pair in cookie.Split(';', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
        {
            var eq = pair.IndexOf('=');
            if (eq <= 0)
            {
                continue;
            }
            try
            {
                Http.DefaultRequestHeaders.Cookie.Add(new HttpCookiePairHeaderValue(pair[..eq], pair[(eq + 1)..]));
            }
            catch
            {
                // One malformed pair must not keep the track from playing.
            }
        }
    }

    private static void Fail(int generation, string code, int ext, string msg)
    {
        if (generation != _generation)
        {
            return;
        }
        Emit(new HelperEvent { Ev = "failed", Code = code, Ext = ext, Msg = msg, Gen = _gen });
    }

    private static void Emit(HelperEvent ev)
    {
        var line = JsonSerializer.Serialize(ev, HelperJsonContext.Default.HelperEvent);
        lock (outLock)
        {
            Out.WriteLine(line);
        }
    }
}

internal sealed class HelperCommand
{
    [JsonPropertyName("cmd")] public string Cmd { get; set; } = "";
    [JsonPropertyName("url")] public string? Url { get; set; }
    [JsonPropertyName("cookie")] public string? Cookie { get; set; }
    [JsonPropertyName("ua")] public string? Ua { get; set; }
    [JsonPropertyName("ms")] public double Ms { get; set; }
    [JsonPropertyName("value")] public double Value { get; set; }
    [JsonPropertyName("gen")] public int Gen { get; set; }
    [JsonPropertyName("play")] public bool Play { get; set; }
}

internal sealed class HelperEvent
{
    [JsonPropertyName("ev")] public string Ev { get; set; } = "";
    [JsonPropertyName("durationMs")] public double? DurationMs { get; set; }
    [JsonPropertyName("ms")] public double? Ms { get; set; }
    [JsonPropertyName("code")] public string? Code { get; set; }
    [JsonPropertyName("ext")] public int? Ext { get; set; }
    [JsonPropertyName("msg")] public string? Msg { get; set; }
    [JsonPropertyName("gen")] public int Gen { get; set; }
}

// Reflection-based JsonSerializer is disabled under NativeAOT; the source
// generator keeps the protocol serializable.
[JsonSourceGenerationOptions(DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull)]
[JsonSerializable(typeof(HelperCommand))]
[JsonSerializable(typeof(HelperEvent))]
internal partial class HelperJsonContext : JsonSerializerContext
{
}
