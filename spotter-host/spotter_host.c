/* spotter-host: drive the vendor Yandex spotter + VQE (libyandex_evil.so) without maind.
 *
 *   spotter-host probe <model_dir>
 *       print conf facts and which channel-type layouts spotter_create_multichannel accepts
 *   spotter-host run [-t TYPES] [-r RATE] [-c CHUNK_MS] <model_dir>  < pcm_s16le_interleaved
 *       feed PCM from stdin, print one line per non-trivial event:
 *       "<seconds> <event_name> [<phrase>]"
 *   spotter-host vqe [-s SCALE] [-A MODE] [-C MODE]  < hw:0,1 capture > pcm16k
 *       run the vendor VQE ("bergamot" preset) on a raw 8ch S16 48 kHz hw:0,1 capture
 *       (mics = ch0,1,2; HW feedback = ch6) and write 16 kHz S16 interleaved
 *       [vqe_0, vqe_bnr, vqe_gain, raw_mic(omni)] to stdout; 1 s level lines on stderr
 *   spotter-host listen [-s SCALE] [-A MODE] [-C MODE] [-x MAP] [-v] -a <activation_model> [-k <command_model>]
 *       hw:0,1 capture -> VQE -> activation spotter (layout 0,3,4 = vqe_0, vqe_bnr, raw mic)
 *       [+ a command spotter on vqe_0]; one line per event:
 *       "<seconds> <tag> <event_name> [<phrase>]"  (tag = WAKE | CMD)
 *       -x MAP skips the VQE: comma list of hw:0,1 channels fed to the spotter at 48 kHz,
 *          in the model's layout order (e.g. -x 0,0,1 for omni=mic0, bnr=mic0, raw=mic1)
 *       -v prints a "LEVEL" line per second (dBFS of the spotter inputs)
 *       -b feeds vqe_0 (not vqe_bnr) into the model's BNR input
 *       -g feeds vqe_gain (vqe_0 x50, clipped) instead of vqe_0 into the command spotter
 *   capture layout (vqe/listen/serve): -P PRESET (vendor VQE preset, default bergamot)
 *       -m DIR (the VQE's model dir, default /system/vendor/quasar/vqe_models; its noise reducer
 *       models are loaded from DIR/noise_reducer/, e.g. for -C 1, the call mode)
 *       -N CHANNELS (interleaved capture channels, default 8) -M MICS (comma list of mic channels in
 *       preset order, default 0,1,2) -R REF (speaker-feedback channel, default 6; -1 = none)
 *   spotter-host serve [listen flags, VQE path only] -a <activation_model> [-k <command_model>]
 *       the yastationd child: stdin = raw hw:0,1 frames (8ch S16 48 kHz, as arecord gives them);
 *       stdout = machine lines, one per event (the daemon ignores lines it doesn't know):
 *         READY call-mode                (capabilities; signals are safe from now on)
 *         WAKE <stream_ms> <phrase>      activation CONFIRMATION_1ST_STAGE
 *         CMD <stream_ms> <phrase>       command CONFIRMATION_1ST_STAGE (always on; the daemon arms)
 *         SUBHIT <WAKE|CMD> <stream_ms> <phrase>
 *         LEVEL <stream_ms> vqe=<dB> raw=<dB> spk=<dB>   (with -v, 1/s)
 *         MODE <n> ok|err                the communication mode switched (SIGUSR1 / SIGUSR2)
 *       fd 3, if open: continuous vqe_0 16 kHz mono S16LE, written non-blocking (dropped when the
 *       reader lags, so the VQE never stalls). Exits on stdin EOF.
 *       fd 4, if open: vqe_gain the same way — in the call mode (SetCommunicationMode 1: neural
 *       NR + AGC) the call's uplink. SIGUSR1 switches the call mode on, SIGUSR2 off (0, the ASR
 *       mode); vqe_0 (the spotters' input) is the same in both (docs/intercom.md). The command
 *       spotter pauses in the call mode (CPU); the activation spotter keeps running.
 *
 * TYPES is a comma list of channel types, one per interleaved channel (default "0" = omni).
 */
#include <errno.h>
#include <fcntl.h>
#include <math.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "spotter_api.h"
#include "vqe_api.h"

static const char *event_names[] = {
    "NEED_MORE_DATA_FOR_1ST_STAGE", "SUBHIT", "CONFIRMATION_1ST_STAGE", "1ST_STAGE_HIT",
    "NEED_MORE_DATA_FOR_2ND_STAGE", "CONFIRMATION_2ND_STAGE", "REJECTION_2ND_STAGE",
    "FINISH", "ACTIVATION_SEAMLESS", "NEED_MORE_DATA_FOR_SEAM_VAD",
};

static const char *event_name(int ev)
{
    if (ev >= 0 && ev < (int)(sizeof(event_names) / sizeof(event_names[0])))
        return event_names[ev];
    return "UNKNOWN";
}

/* Print and free *err if set; returns 1 if there was an error. */
static int report(const char *what, spotter_error **err)
{
    if (!*err)
        return 0;
    if (!spotter_error_get_message) {
        /* 3.21 blob: no error helpers, and the slot holds the library's WARN-log buffer (a plain
         * C string, owned by the library), not an error object. The call's NULL return is the
         * real failure signal; the buffered warnings help diagnose it. */
        fprintf(stderr, "%s: error: %s\n", what, (const char *)*err);
        *err = NULL;
        return 1;
    }
    fprintf(stderr, "%s: error type=%d func=%s: %s\n", what, spotter_error_get_type(*err),
            spotter_error_get_func_name(*err), spotter_error_get_message(*err));
    spotter_error_free(err);
    *err = NULL;
    return 1;
}

static int parse_types(const char *s, int *types, int max)
{
    int n = 0;
    char *end;
    while (*s && n < max) {
        types[n++] = (int)strtol(s, &end, 10);
        if (*end != ',')
            break;
        s = end + 1;
    }
    return n;
}

static int cmd_probe(const char *dir)
{
    spotter_error *err = NULL;
    spotter_conf *conf = spotter_conf_read(dir, &err);
    if (report("spotter_conf_read", &err) || !conf)
        return 1;

    int rate = spotter_conf_native_sample_rate(conf, &err);
    report("spotter_conf_native_sample_rate", &err);
    printf("native_sample_rate %d\n", rate);

    for (int what = 0; what < 6; what++) {
        int n = spotter_conf_get_required_channel_count(conf, what, &err);
        if (!report("spotter_conf_get_required_channel_count", &err))
            printf("required_channel_count(%d) %d\n", what, n);
    }

    /* Single-channel create. */
    spotter *s = spotter_create(conf, rate, NULL, &err);
    if (!report("spotter_create", &err) && s) {
        printf("spotter_create ok\n");
        spotter_destroy(s, &err);
        report("spotter_destroy", &err);
    }

    /* Multichannel: one channel of every type the conf requires, in type order
     * (types 0..5 are accepted by the library; 6+ give "Unknown channel type"). */
    int types[16], n = 0;
    for (int t = 0; t < 6; t++) {
        int k = spotter_conf_get_required_channel_count(conf, t, &err);
        report("spotter_conf_get_required_channel_count", &err);
        for (int i = 0; i < k && n < 16; i++)
            types[n++] = t;
    }
    printf("layout");
    for (int i = 0; i < n; i++)
        printf("%s%d", i ? "," : " ", types[i]);
    printf("\n");
    s = spotter_create_multichannel(conf, rate, n, types, NULL, &err);
    if (!report("spotter_create_multichannel", &err) && s) {
        printf("spotter_create_multichannel ok\n");
        spotter_destroy(s, &err);
        report("spotter_destroy", &err);
    }

    spotter_conf_free(conf, &err);
    report("spotter_conf_free", &err);
    return 0;
}

static int cmd_run(const char *dir, const int *types, int ntypes, int rate, int chunk_ms)
{
    spotter_error *err = NULL;
    spotter_conf *conf = spotter_conf_read(dir, &err);
    if (report("spotter_conf_read", &err) || !conf)
        return 1;
    if (rate == 0)
        rate = spotter_conf_native_sample_rate(conf, &err);
    if (report("spotter_conf_native_sample_rate", &err))
        return 1;

    spotter *s = ntypes == 1 && types[0] == 0
                     ? spotter_create(conf, rate, NULL, &err)
                     : spotter_create_multichannel(conf, rate, ntypes, types, NULL, &err);
    if (report("spotter_create", &err) || !s)
        return 1;

    size_t frame = 2 * (size_t)ntypes;
    size_t chunk = (size_t)rate * chunk_ms / 1000 * frame;
    unsigned char *buf = malloc(chunk);
    unsigned long long frames_in = 0;
    if (!buf)
        return 1;

    for (;;) {
        size_t got = 0;
        while (got < chunk) {
            ssize_t r = read(0, buf + got, chunk - got);
            if (r < 0 && errno == EINTR)
                continue;
            if (r <= 0)
                break;
            got += (size_t)r;
        }
        got -= got % frame;
        if (got == 0)
            break;

        spotter_submit_raw_data(s, buf, got, &err);
        if (report("spotter_submit_raw_data", &err))
            break;
        frames_in += got / frame;

        for (;;) {
            int ev = spotter_get_next_event(s, &err);
            if (report("spotter_get_next_event", &err))
                goto out;
            if (ev == SPOTTER_NEED_MORE_DATA_FOR_1ST_STAGE ||
                ev == SPOTTER_NEED_MORE_DATA_FOR_2ND_STAGE ||
                ev == SPOTTER_NEED_MORE_DATA_FOR_SEAM_VAD)
                break;
            const char *phrase = NULL;
            if (ev == SPOTTER_SUBHIT || ev == SPOTTER_CONFIRMATION_1ST_STAGE ||
                ev == SPOTTER_1ST_STAGE_HIT || ev == SPOTTER_CONFIRMATION_2ND_STAGE) {
                phrase = spotter_get_current_phrase(s, &err);
                report("spotter_get_current_phrase", &err);
            }
            printf("%.2f %s%s%s\n", (double)frames_in / rate, event_name(ev),
                   phrase ? " " : "", phrase ? phrase : "");
            fflush(stdout);
            if (ev == SPOTTER_FINISH)
                goto out;
        }
    }
out:
    free(buf);
    spotter_destroy(s, &err);
    report("spotter_destroy", &err);
    spotter_conf_free(conf, &err);
    report("spotter_conf_free", &err);
    return 0;
}

/* ---------------------------------------------------------------- VQE front-end */

/* Capture layout + VQE preset: the defaults are bergamot's (hw:0,1 = 8 channels, mics 0-2,
 * amp feedback on 6); other boards pass -P/-N/-M/-R/-Q (yastationd takes them from its board
 * profile). -R is the single speaker-feedback channel (presets with nspk == 1); -Q is the
 * full list for multi-ref presets (nspk > 1, e.g. orion's 7), in VQE speaker order. */
#define MAX_MICS 8
#define MAX_SPK 8
static const char *vqe_preset = "bergamot";
static const char *vqe_models = "/system/vendor/quasar/vqe_models"; /* -m: the VQE's model dir */
static int HW_CH = 8;      /* interleaved channels in the capture */
static int HW_SPK_CH = 6;  /* speaker feedback (AEC reference); -1 = none (zeros) */
static int hw_mic_ch[MAX_MICS] = {0, 1, 2};
static int hw_nmic = 3;
static int hw_spk_ch[MAX_SPK] = {-1, -1, -1, -1, -1, -1, -1, -1}; /* -Q list; [0] mirrors -R */
static int hw_nspk = 0;    /* 0 = use HW_SPK_CH (legacy single-ref) */

struct fe {
    YandexVqeC_Config *conf;
    YandexVqeC_Vqe *vqe;
    int chunk;  /* input frames per Process() at 48 kHz */
    int nmic, nspk;
    float scale; /* float = s16 * scale on input; s16 = float / scale on output */
    short *in;
    float *mic, *spk;
};

static int vqe_err(const char *what, YandexVqeC_Error *e)
{
    if (!e)
        return 0;
    fprintf(stderr, "%s: %s\n", what, YandexVqeC_Error_GetMessage(e));
    YandexVqeC_Error_Free(e);
    return 1;
}

static int fe_open(struct fe *f, float scale, int asr_mode, int comm_mode)
{
    memset(f, 0, sizeof(*f));
    f->scale = scale;
    if (vqe_err("Config_CreateFromPreset", YandexVqeC_Config_CreateFromPreset(vqe_preset, &f->conf)))
        return 1;
    f->nmic = YandexVqeC_Config_GetMicsCount(f->conf);
    f->nspk = YandexVqeC_Config_GetSpeakersCount(f->conf);
    f->chunk = YandexVqeC_Config_GetInputChunkSize(f->conf);
    fprintf(stderr, "vqe: mics %d spk %d in_rate %d out_rate %d frame %d chunk %d\n", f->nmic, f->nspk,
            YandexVqeC_Config_GetInputSamplingRate(f->conf),
            YandexVqeC_Config_GetOutputSamplingRate(f->conf),
            YandexVqeC_Config_GetProcessingFrame(f->conf), f->chunk);
    for (int t = 1; t <= 9; t++) {
        size_t n = 0;
        if (!vqe_err("GetOutputChannelCount", YandexVqeC_Config_GetOutputChannelCount(f->conf, t, &n)))
            fprintf(stderr, "vqe: out_channels(%d) %zu\n", t, n);
    }
    if (f->nmic != hw_nmic || f->nmic > MAX_MICS || f->nspk < 1 || f->nspk > MAX_SPK || f->chunk <= 0) {
        fprintf(stderr, "vqe: preset %s wants %d mics/%d spk, -M gives %d mics\n", vqe_preset, f->nmic, f->nspk,
                hw_nmic);
        return 1;
    }
    if (f->nspk == 1) { /* single-ref presets resolve from -R (legacy) */
        hw_spk_ch[0] = HW_SPK_CH;
        hw_nspk = 1;
    } else if (hw_nspk != f->nspk) {
        fprintf(stderr, "vqe: preset %s wants %d spk refs (-Q must give exactly that)\n", vqe_preset, f->nspk);
        return 1;
    }
    for (int i = 0; i < hw_nspk; i++)
        if (hw_spk_ch[i] < -1 || hw_spk_ch[i] >= HW_CH) {
            fprintf(stderr, "vqe: -Q ref channel %d out of range\n", hw_spk_ch[i]);
            return 1;
        }
    if (vqe_err("Create", YandexVqeC_Create(f->conf, vqe_models, 0, NULL, &f->vqe)))
        return 1;
    if (asr_mode >= 0)
        vqe_err("SetASRMode", YandexVqeC_SetASRMode(f->vqe, asr_mode));
    if (comm_mode >= 0)
        vqe_err("SetCommunicationMode", YandexVqeC_SetCommunicationMode(f->vqe, comm_mode));
    f->in = malloc(sizeof(short) * HW_CH * f->chunk);
    f->mic = malloc(sizeof(float) * f->nmic * f->chunk);
    f->spk = malloc(sizeof(float) * f->nspk * f->chunk);
    return !f->in || !f->mic || !f->spk;
}

static size_t read_full(void *buf, size_t n)
{
    size_t got = 0;
    while (got < n) {
        ssize_t r = read(0, (char *)buf + got, n - got);
        if (r < 0 && errno == EINTR)
            continue;
        if (r <= 0)
            break;
        got += (size_t)r;
    }
    return got;
}

/* Read one chunk of interleaved capture and run it through the VQE. 0 = ok, 1 = EOF/error. */
static int fe_step(struct fe *f)
{
    size_t need = sizeof(short) * HW_CH * f->chunk;
    if (read_full(f->in, need) != need)
        return 1;
    for (int i = 0; i < f->chunk; i++) {
        const short *fr = f->in + i * HW_CH;
        for (int m = 0; m < f->nmic; m++)
            f->mic[i * f->nmic + m] = fr[hw_mic_ch[m]] * f->scale;
        for (int s = 0; s < f->nspk; s++) /* -Q uses -1 to feed silence for a ref */
            f->spk[i * f->nspk + s] = hw_spk_ch[s] >= 0 ? fr[hw_spk_ch[s]] * f->scale : 0.f;
    }
    return vqe_err("Process", YandexVqeC_Process(f->vqe, f->mic, (size_t)f->chunk * f->nmic, f->spk,
                                                 (size_t)f->chunk * f->nspk));
}

static const float *fe_out(struct fe *f, int type, int idx, size_t *n)
{
    const float *p = NULL;
    *n = 0;
    if (vqe_err("GetOutputChannelData", YandexVqeC_GetOutputChannelData(f->vqe, type, idx, &p, n)))
        return NULL;
    return p;
}

static short to_s16(float v, float scale)
{
    float x = v / scale;
    if (x > 32767.f)
        return 32767;
    if (x < -32768.f)
        return -32768;
    return (short)lrintf(x);
}

static double dbfs(double sumsq, unsigned long n)
{
    return n ? 10.0 * log10(sumsq / n / (32768.0 * 32768.0) + 1e-20) : -200.0;
}

/* Interleave the spotter inputs [vqe_0, vqe_bnr, gain, raw_mic(omni)] into out (S16, 16 kHz).
 * Returns frames written. */
static size_t fe_collect(struct fe *f, short *out, size_t max_frames, int *omni)
{
    size_t n0, n1, n2, n3;
    int om = 0;
    vqe_err("GetOmniMicIndex", YandexVqeC_GetOmniMicIndex(f->vqe, &om));
    if (om < 0 || om >= f->nmic)
        om = 0;
    *omni = om;
    const float *v = fe_out(f, VQE_CH_VQE, 0, &n0);
    const float *b = fe_out(f, VQE_CH_VQE_BNR, 0, &n1);
    const float *g = fe_out(f, VQE_CH_VQE_GAIN, 0, &n2);
    const float *r = fe_out(f, VQE_CH_RAW_MIC, om, &n3);
    size_t n = n0;
    if (n > max_frames)
        n = max_frames;
    for (size_t i = 0; i < n; i++) {
        out[i * 4 + 0] = v ? to_s16(v[i], f->scale) : 0;
        out[i * 4 + 1] = b && i < n1 ? to_s16(b[i], f->scale) : 0;
        out[i * 4 + 2] = g && i < n2 ? to_s16(g[i], f->scale) : 0;
        out[i * 4 + 3] = r && i < n3 ? to_s16(r[i], f->scale) : 0;
    }
    return n;
}

static int cmd_vqe(float scale, int asr_mode, int comm_mode)
{
    struct fe f;
    if (fe_open(&f, scale, asr_mode, comm_mode))
        return 1;
    short out[4 * 1024];
    double acc[4] = {0}, accin[4] = {0};
    unsigned long nacc = 0, nin = 0, chunks = 0;
    while (!fe_step(&f)) {
        int om;
        size_t n = fe_collect(&f, out, 1024, &om);
        if (chunks == 0) {
            size_t a, b, c, d;
            fe_out(&f, VQE_CH_VQE, 0, &a);
            fe_out(&f, VQE_CH_VQE_BNR, 0, &b);
            fe_out(&f, VQE_CH_VQE_GAIN, 0, &c);
            fe_out(&f, VQE_CH_RAW_MIC, 0, &d);
            fprintf(stderr, "vqe: floats per chunk vqe %zu bnr %zu gain %zu raw_mic %zu\n", a, b, c, d);
        }
        fwrite(out, sizeof(short) * 4, n, stdout);
        for (size_t i = 0; i < n; i++)
            for (int k = 0; k < 4; k++)
                acc[k] += (double)out[i * 4 + k] * out[i * 4 + k];
        nacc += n;
        for (int i = 0; i < f.chunk; i++) {
            for (int m = 0; m < 3 && m < hw_nmic; m++)
                accin[m] += (double)f.in[i * HW_CH + hw_mic_ch[m]] * f.in[i * HW_CH + hw_mic_ch[m]];
            if (HW_SPK_CH >= 0)
                accin[3] += (double)f.in[i * HW_CH + HW_SPK_CH] * f.in[i * HW_CH + HW_SPK_CH];
        }
        nin += f.chunk;
        if (++chunks % 62 == 0) { /* ~1 s at 16 ms chunks */
            fprintf(stderr,
                    "%.1f in mic0 %.1f mic1 %.1f mic2 %.1f spk %.1f | out vqe %.1f bnr %.1f gain %.1f raw %.1f dBFS omni=%d\n",
                    chunks * 0.016, dbfs(accin[0], nin), dbfs(accin[1], nin), dbfs(accin[2], nin),
                    dbfs(accin[3], nin), dbfs(acc[0], nacc), dbfs(acc[1], nacc), dbfs(acc[2], nacc),
                    dbfs(acc[3], nacc), om);
            memset(acc, 0, sizeof(acc));
            memset(accin, 0, sizeof(accin));
            nacc = nin = 0;
        }
    }
    fflush(stdout);
    YandexVqeC_Free(f.vqe);
    YandexVqeC_Config_Free(f.conf);
    return 0;
}

/* ---------------------------------------------------------------- listen */

static int serve_mode; /* serve: machine-readable lines + vqe_0 on fd 3 */
static volatile sig_atomic_t want_comm = -1; /* serve: SIGUSR1 = 1 (call), SIGUSR2 = 0 (ASR) */

static void on_usr(int sig) { want_comm = sig == SIGUSR1 ? 1 : 0; }

struct det {
    const char *tag;
    spotter_conf *conf;
    spotter *s;
    int ntypes, types[8];
    short *buf; /* interleaved per its layout */
};

static int det_open(struct det *d, const char *tag, const char *dir, int rate)
{
    spotter_error *err = NULL;
    memset(d, 0, sizeof(*d));
    d->tag = tag;
    d->conf = spotter_conf_read(dir, &err);
    if (report("spotter_conf_read", &err) || !d->conf)
        return 1;
    for (int t = 0; t < 6; t++) {
        int k = spotter_conf_get_required_channel_count(d->conf, t, &err);
        report("spotter_conf_get_required_channel_count", &err);
        for (int i = 0; i < k && d->ntypes < 8; i++)
            d->types[d->ntypes++] = t;
    }
    d->s = spotter_create_multichannel(d->conf, rate, d->ntypes, d->types, NULL, &err);
    if (report("spotter_create_multichannel", &err) || !d->s)
        return 1;
    fprintf(stderr, "%s: %s layout", tag, dir);
    for (int i = 0; i < d->ntypes; i++)
        fprintf(stderr, "%s%d", i ? "," : " ", d->types[i]);
    fprintf(stderr, " rate %d\n", rate);
    d->buf = malloc(sizeof(short) * d->ntypes * 4096);
    return !d->buf;
}

/* Feed nframes (already interleaved in d->buf) and print events. */
static void det_feed(struct det *d, size_t nframes, double t)
{
    spotter_error *err = NULL;
    spotter_submit_raw_data(d->s, d->buf, nframes * 2 * d->ntypes, &err);
    if (report("spotter_submit_raw_data", &err))
        return;
    for (;;) {
        int ev = spotter_get_next_event(d->s, &err);
        if (report("spotter_get_next_event", &err))
            return;
        if (ev == SPOTTER_NEED_MORE_DATA_FOR_1ST_STAGE || ev == SPOTTER_NEED_MORE_DATA_FOR_2ND_STAGE ||
            ev == SPOTTER_NEED_MORE_DATA_FOR_SEAM_VAD || ev == SPOTTER_FINISH)
            return;
        const char *phrase = NULL;
        if (ev == SPOTTER_SUBHIT || ev == SPOTTER_CONFIRMATION_1ST_STAGE || ev == SPOTTER_1ST_STAGE_HIT ||
            ev == SPOTTER_CONFIRMATION_2ND_STAGE) {
            phrase = spotter_get_current_phrase(d->s, &err);
            report("spotter_get_current_phrase", &err);
        }
        if (serve_mode) {
            long ms = (long)(t * 1000);
            if (ev == SPOTTER_CONFIRMATION_1ST_STAGE)
                printf("%s %ld %s\n", d->tag, ms, phrase ? phrase : "");
            else if (ev == SPOTTER_SUBHIT)
                printf("SUBHIT %s %ld %s\n", d->tag, ms, phrase ? phrase : "");
            continue;
        }
        printf("%.2f %s %s%s%s\n", t, d->tag, event_name(ev), phrase ? " " : "", phrase ? phrase : "");
    }
}

/* Map a spotter channel type to the VQE output column in fe_collect's 4-wide frame. */
static int bnr_from_vqe; /* -b: feed vqe_0 into the BNR input instead of vqe_bnr */
static int cmd_gain;     /* -g: feed vqe_gain (x50) instead of vqe_0 to the command spotter */

static int vqe_col(const struct det *d, int type)
{
    switch (type) {
    case 3: return bnr_from_vqe ? 0 : 1; /* BNR  <- vqe_bnr (or vqe_0 with -b) */
    case 4: return 3; /* raw  <- raw_mic(omni) */
    default: return cmd_gain && d->tag[0] == 'C' ? 2 : 0; /* omni <- vqe_0 (or vqe_gain) */
    }
}

static int cmd_listen(const char *amodel, const char *kmodel, float scale, int asr_mode, int comm_mode,
                      const int *xmap, int nx, int verbose)
{
    struct det dets[2];
    int nd = 0;
    int rate = nx ? 48000 : 16000;
    if (amodel && det_open(&dets[nd++], "WAKE", amodel, rate))
        return 1;
    if (kmodel && det_open(&dets[nd++], "CMD", kmodel, rate))
        return 1;
    if (!nd)
        return 2;

    double lev[8] = {0};
    unsigned long nlev = 0, frames = 0, next_lev = (unsigned long)rate;

    if (nx) { /* no VQE: pick hw channels straight into the spotter at 48 kHz */
        for (int k = 0; k < nd; k++)
            if (dets[k].ntypes > nx) {
                fprintf(stderr, "%s needs %d channels, -x gives %d\n", dets[k].tag, dets[k].ntypes, nx);
                return 2;
            }
        const size_t chunk = 4800; /* 100 ms */
        short *in = malloc(sizeof(short) * HW_CH * chunk);
        for (;;) {
            size_t got = read_full(in, sizeof(short) * HW_CH * chunk) / (sizeof(short) * HW_CH);
            if (!got)
                break;
            for (int k = 0; k < nd; k++) {
                struct det *d = &dets[k];
                for (size_t done = 0; done < got;) {
                    size_t n = got - done > 4096 ? 4096 : got - done;
                    for (size_t i = 0; i < n; i++)
                        for (int c = 0; c < d->ntypes; c++)
                            d->buf[i * d->ntypes + c] = in[(done + i) * HW_CH + xmap[c]];
                    det_feed(d, n, (double)(frames + done + n) / rate);
                    done += n;
                }
            }
            for (size_t i = 0; i < got; i++)
                for (int c = 0; c < nx; c++)
                    lev[c] += (double)in[i * HW_CH + xmap[c]] * in[i * HW_CH + xmap[c]];
            nlev += got;
            frames += got;
            if (verbose && frames >= next_lev) {
                printf("%.2f LEVEL", (double)frames / rate);
                for (int c = 0; c < nx; c++)
                    printf(" ch%d %.1f", xmap[c], dbfs(lev[c], nlev));
                printf("\n");
                memset(lev, 0, sizeof(lev));
                nlev = 0;
                next_lev += (unsigned long)rate;
            }
            if (got < chunk)
                break;
        }
        return 0;
    }

    struct fe f;
    if (fe_open(&f, scale, asr_mode, comm_mode))
        return 1;
    short out[4 * 1024], mono[1024];
    int om = 0;
    int vfd = -1, gfd = -1, cur_comm = comm_mode;
    if (serve_mode) {
        if (fcntl(3, F_GETFD) != -1) { /* the daemon passed a pipe for vqe_0 */
            vfd = 3;
            fcntl(vfd, F_SETFL, fcntl(vfd, F_GETFL) | O_NONBLOCK);
            signal(SIGPIPE, SIG_IGN);
        }
        if (fcntl(4, F_GETFD) != -1) { /* ... and one for vqe_gain (the intercom's uplink) */
            gfd = 4;
            fcntl(gfd, F_SETFL, fcntl(gfd, F_GETFL) | O_NONBLOCK);
            signal(SIGPIPE, SIG_IGN);
        }
        signal(SIGUSR1, on_usr);
        signal(SIGUSR2, on_usr);
        printf("READY call-mode\n"); /* the handlers are in: the daemon may signal now */
    }
    while (!fe_step(&f)) {
        if (serve_mode && want_comm >= 0 && want_comm != cur_comm) {
            /* between chunks: the VQE reconfigures itself (the NR model loads on the first switch) */
            cur_comm = want_comm;
            int bad = vqe_err("SetCommunicationMode", YandexVqeC_SetCommunicationMode(f.vqe, cur_comm));
            printf("MODE %d %s\n", cur_comm, bad ? "err" : "ok");
        }
        size_t n = fe_collect(&f, out, 1024, &om);
        frames += n;
        if (vfd >= 0) {
            for (size_t i = 0; i < n; i++)
                mono[i] = out[i * 4 + 0];
            if (write(vfd, mono, n * sizeof(short)) < 0 && errno != EAGAIN)
                vfd = -1; /* reader gone: stop writing, keep spotting */
        }
        if (gfd >= 0) {
            for (size_t i = 0; i < n; i++)
                mono[i] = out[i * 4 + 2];
            if (write(gfd, mono, n * sizeof(short)) < 0 && errno != EAGAIN)
                gfd = -1;
        }
        for (int k = 0; k < nd; k++) {
            struct det *d = &dets[k];
            /* In the call mode the command spotter rests: a call doesn't use it, and the VQE's
             * neural NR takes its share of this single core (the stream fell behind real time). */
            if (cur_comm == 1 && d->tag[0] == 'C')
                continue;
            for (size_t i = 0; i < n; i++)
                for (int c = 0; c < d->ntypes; c++)
                    d->buf[i * d->ntypes + c] = out[i * 4 + vqe_col(d, d->types[c])];
            det_feed(d, n, (double)frames / rate);
        }
        for (size_t i = 0; i < n; i++)
            for (int c = 0; c < 4; c++)
                lev[c] += (double)out[i * 4 + c] * out[i * 4 + c];
        for (int i = 0; i < f.chunk; i++) /* HW feedback level at 48 kHz, normalised to n below */
            if (HW_SPK_CH >= 0)
                lev[4] += (double)f.in[i * HW_CH + HW_SPK_CH] * f.in[i * HW_CH + HW_SPK_CH] * n / f.chunk;
        nlev += n;
        if (verbose && serve_mode && frames >= next_lev) {
            printf("LEVEL %ld vqe=%.1f raw=%.1f spk=%.1f\n", (long)((double)frames / rate * 1000),
                   dbfs(lev[0], nlev), dbfs(lev[3], nlev), dbfs(lev[4], nlev));
            memset(lev, 0, sizeof(lev));
            nlev = 0;
            next_lev += (unsigned long)rate;
        } else if (verbose && frames >= next_lev) {
            printf("%.2f LEVEL vqe %.1f bnr %.1f gain %.1f raw %.1f spk %.1f omni %d\n",
                   (double)frames / rate, dbfs(lev[0], nlev), dbfs(lev[1], nlev), dbfs(lev[2], nlev),
                   dbfs(lev[3], nlev), dbfs(lev[4], nlev), om);
            memset(lev, 0, sizeof(lev));
            nlev = 0;
            next_lev += (unsigned long)rate;
        }
    }
    return 0;
}

static void usage(void)
{
    fprintf(stderr, "usage: spotter-host probe <model_dir>\n"
                    "       spotter-host run [-t TYPES] [-r RATE] [-c CHUNK_MS] <model_dir> < pcm\n"
                    "       spotter-host vqe [-s SCALE] [-A MODE] [-C MODE] [-P PRESET] [-N CH] [-M MICS] [-R CH | -Q CHLIST]"
                    " < capture > pcm16k_4ch\n"
                    "       spotter-host listen [-s SCALE] [-A MODE] [-C MODE] [-P PRESET] [-N CH] [-M MICS] [-R CH | -Q CHLIST]"
                    " [-x MAP] [-v] [-b] [-g] -a MODEL [-k MODEL] < capture\n"
                    "       spotter-host serve [...] -a MODEL [-k MODEL] < capture [3> vqe0_16k_mono]\n"
                    "   -P preset   vendor VQE preset (default bergamot; orion takes the raw 13ch 96 kHz mroute stream)\n"
                    "   -N CH       interleaved channels in the capture (default 8)\n"
                    "   -M MICS     capture channels of the VQE mics, comma list in preset order (default 0,1,2)\n"
                    "   -R CH       single speaker-ref channel (presets with 1 ref; default 6; -1 = silence)\n"
                    "   -Q CHLIST   all speaker-ref channels in VQE order (presets with >1 ref, e.g. orion's 7;"
                    " count must match the preset; -1 entries feed silence)\n");
    exit(2);
}

int main(int argc, char **argv)
{
    setvbuf(stdout, NULL, _IOLBF, 0);
    if (argc < 2)
        usage();
    int types[8] = {0}, ntypes = 1, rate = 0, chunk_ms = 100, opt, asr = -1, comm = -1, verbose = 0, nx = 0;
    float scale = 1.0f / 32768.0f;
    const char *amodel = NULL, *kmodel = NULL;
    if (strcmp(argv[1], "probe") == 0 && argc >= 3)
        return cmd_probe(argv[2]);
    optind = 2;
    if (strcmp(argv[1], "vqe") == 0 || strcmp(argv[1], "listen") == 0 || strcmp(argv[1], "serve") == 0) {
        while ((opt = getopt(argc, argv, "s:A:C:a:k:x:vbgP:N:M:R:Q:m:")) != -1) {
            switch (opt) {
            case 's': scale = (float)atof(optarg); break;
            case 'A': asr = atoi(optarg); break;
            case 'C': comm = atoi(optarg); break;
            case 'a': amodel = optarg; break;
            case 'k': kmodel = optarg; break;
            case 'x': nx = ntypes = parse_types(optarg, types, 8); break;
            case 'v': verbose = 1; break;
            case 'b': bnr_from_vqe = 1; break;
            case 'g': cmd_gain = 1; break;
            case 'P': vqe_preset = optarg; break;
            case 'm': vqe_models = optarg; break;
            case 'N': HW_CH = atoi(optarg); break;
            case 'M': hw_nmic = parse_types(optarg, hw_mic_ch, MAX_MICS); break;
            case 'R': HW_SPK_CH = atoi(optarg); break;
            case 'Q': hw_nspk = parse_types(optarg, hw_spk_ch, MAX_SPK); break;
            default: usage();
            }
        }
        if (scale <= 0 || HW_CH < 1 || HW_CH > 32 || hw_nmic < 1 || HW_SPK_CH >= HW_CH)
            usage();
        for (int i = 0; i < hw_nmic; i++)
            if (hw_mic_ch[i] < 0 || hw_mic_ch[i] >= HW_CH)
                usage();
        if (argv[1][0] == 'v')
            return cmd_vqe(scale, asr, comm);
        if (!amodel && !kmodel)
            usage();
        if (argv[1][0] == 's') {
            if (nx)
                usage(); /* serve runs the VQE path */
            serve_mode = 1;
        }
        for (int i = 0; i < ntypes; i++)
            if (types[i] < 0 || types[i] >= HW_CH)
                usage();
        return cmd_listen(amodel, kmodel, scale, asr, comm, types, nx, verbose);
    }
    if (strcmp(argv[1], "run") != 0 || argc < 3)
        usage();
    while ((opt = getopt(argc, argv, "t:r:c:")) != -1) {
        switch (opt) {
        case 't': ntypes = parse_types(optarg, types, 8); break;
        case 'r': rate = atoi(optarg); break;
        case 'c': chunk_ms = atoi(optarg); break;
        default: usage();
        }
    }
    if (optind >= argc || ntypes < 1 || chunk_ms < 10)
        usage();
    return cmd_run(argv[optind], types, ntypes, rate, chunk_ms);
}
