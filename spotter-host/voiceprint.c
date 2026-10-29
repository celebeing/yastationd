/* voiceprint: the firmware's own voice biometry (bio_lingware) without maind, for
 * `spotter-host voice <bio_lingware_dir>` (docs/voice-id.md).
 *
 * Kaldi log-mel fbank (features_config.json: 16 kHz, 40 ms frames every 24 ms, pre-emphasis 0.97,
 * DC removal, Hamming, 1024-point power spectrum, 80 mel bins, snip edges; no dither) -> the
 * ECAPA-TDNN body in blocks of 20 frames (0.48 s; [1,20,80] -> [1,20,128]) -> the head over the
 * last 300 body rows (7.2 s; [1,N,128] -> [1,512]). TFLite is the firmware's libtflite.so (its C
 * API), loaded with dlopen. The gender and age classifiers are not run here: the vendor's op
 * resolver lacks their LOGISTIC op, and they are two small dense layers, which the daemon computes
 * from the .tflite files itself.
 *
 * Protocol (stdin -> stdout, one process serves one utterance at a time):
 *   a <n>\n<n bytes>   append n bytes of 16 kHz mono S16LE (the body runs as blocks fill up)
 *   v <tag>\n          voiceprint over what was appended since the last reset:
 *                        VOICE <tag> <secs> <512 values>
 *                        VOICE <tag> <secs> short          (less than one body block)
 *   r\n                reset (a new utterance)
 *   READY <tag>        printed once the models are loaded (tag = the frontend's compatibility tag)
 *   ERR <text>         a request failed; the process goes on
 */
#include <dlfcn.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define VP_RATE 16000
#define VP_FL 640   /* 40 ms */
#define VP_FS 384   /* 24 ms */
#define VP_NFFT 1024
#define VP_NMEL 80
#define VP_BLOCK 20 /* fbank frames per body call */
#define VP_ROW 128
#define VP_HEAD 300 /* body rows the head looks at */
#define VP_DIM 512

/* The TFLite C API, as libtflite.so exports it. */
typedef struct TfLiteModel TfLiteModel;
typedef struct TfLiteInterpreterOptions TfLiteInterpreterOptions;
typedef struct TfLiteInterpreter TfLiteInterpreter;
typedef struct TfLiteTensor TfLiteTensor;
static struct {
    TfLiteModel *(*model_from_file)(const char *);
    TfLiteInterpreterOptions *(*opts_create)(void);
    void (*opts_threads)(TfLiteInterpreterOptions *, int);
    /* The vendor's build registers no ops in TfLiteInterpreterCreate: interpreters come from its
     * own resolver (TfLiteCustomOpResolverCreate(tag), tag = the frontend's custom-opresolver-tag;
     * signatures read from the library's code, as libyandex_evil calls them). */
    void *(*resolver)(const char *);
    TfLiteInterpreter *(*create)(const TfLiteModel *, void *, const TfLiteInterpreterOptions *);
    int (*resize)(TfLiteInterpreter *, int, const int *, int);
    int (*alloc)(TfLiteInterpreter *);
    TfLiteTensor *(*input)(const TfLiteInterpreter *, int);
    const TfLiteTensor *(*output)(const TfLiteInterpreter *, int);
    int (*invoke)(TfLiteInterpreter *);
    int (*copy_in)(TfLiteTensor *, const void *, size_t);
    int (*copy_out)(const TfLiteTensor *, void *, size_t);
} tfl;

static int tfl_load(void)
{
    void *h = dlopen("libtflite.so", RTLD_NOW | RTLD_GLOBAL);
    if (!h) {
        fprintf(stderr, "voice: %s\n", dlerror());
        return -1;
    }
#define SYM(f, n)                                              \
    if (!(*(void **)&tfl.f = dlsym(h, n))) {                   \
        fprintf(stderr, "voice: libtflite.so lacks %s\n", n); \
        return -1;                                             \
    }
    SYM(model_from_file, "TfLiteModelCreateFromFile");
    SYM(opts_create, "TfLiteInterpreterOptionsCreate");
    SYM(opts_threads, "TfLiteInterpreterOptionsSetNumThreads");
    SYM(resolver, "TfLiteCustomOpResolverCreate");
    SYM(create, "TfLiteInterpreterCreateWithCustomResolver");
    SYM(resize, "TfLiteInterpreterResizeInputTensor");
    SYM(alloc, "TfLiteInterpreterAllocateTensors");
    SYM(input, "TfLiteInterpreterGetInputTensor");
    SYM(output, "TfLiteInterpreterGetOutputTensor");
    SYM(invoke, "TfLiteInterpreterInvoke");
    SYM(copy_in, "TfLiteTensorCopyFromBuffer");
    SYM(copy_out, "TfLiteTensorCopyToBuffer");
#undef SYM
    return 0;
}

/* tag: the frontend's custom-opresolver-tag ("next"). */
static TfLiteInterpreter *tfl_open(const char *dir, const char *rel, const char *tag)
{
    char p[1024];
    snprintf(p, sizeof p, "%s/%s", dir, rel);
    TfLiteModel *m = tfl.model_from_file(p);
    if (!m) {
        fprintf(stderr, "voice: cannot load %s\n", p);
        return NULL;
    }
    void *resolver = tfl.resolver(tag);
    TfLiteInterpreterOptions *o = tfl.opts_create();
    tfl.opts_threads(o, 1);
    TfLiteInterpreter *it = tfl.create(m, resolver, o);
    if (!it || tfl.alloc(it) != 0) {
        fprintf(stderr, "voice: cannot set up %s\n", p);
        return NULL;
    }
    return it;
}

/* in -> out through a one-input, one-output model; 0 on success. */
static int tfl_run(TfLiteInterpreter *it, const float *in, size_t nin, float *out, size_t nout)
{
    if (tfl.copy_in(tfl.input(it, 0), in, nin * sizeof(float)) != 0 || tfl.invoke(it) != 0)
        return -1;
    return tfl.copy_out(tfl.output(it, 0), out, nout * sizeof(float));
}

/* --- fbank --- */

static float win[VP_FL];
static float banks[VP_NMEL][VP_NFFT / 2 + 1];
static int bank_lo[VP_NMEL], bank_hi[VP_NMEL]; /* nonzero range of each bank */

static double mel(double f) { return 1127.0 * log(1.0 + f / 700.0); }

static void fbank_init(void)
{
    for (int i = 0; i < VP_FL; i++)
        win[i] = (float)(0.54 - 0.46 * cos(2 * M_PI * i / (VP_FL - 1)));
    double lo = mel(0), hi = mel(VP_RATE / 2.0), d = (hi - lo) / (VP_NMEL + 1);
    for (int m = 0; m < VP_NMEL; m++) {
        double l = lo + m * d, c = lo + (m + 1) * d, r = lo + (m + 2) * d;
        bank_lo[m] = VP_NFFT / 2;
        bank_hi[m] = 0;
        for (int k = 0; k < VP_NFFT / 2; k++) { /* the Nyquist bin stays 0, as in Kaldi */
            double f = mel((double)k * VP_RATE / VP_NFFT), up = (f - l) / (c - l), down = (r - f) / (r - c);
            double w = up < down ? up : down;
            banks[m][k] = w > 0 ? (float)w : 0;
            if (w > 0) {
                if (k < bank_lo[m])
                    bank_lo[m] = k;
                bank_hi[m] = k;
            }
        }
    }
}

/* In-place radix-2 complex FFT of n points. */
static void fft(double *re, double *im, int n)
{
    for (int i = 1, j = 0; i < n; i++) {
        int bit = n >> 1;
        for (; j & bit; bit >>= 1)
            j ^= bit;
        j ^= bit;
        if (i < j) {
            double t = re[i]; re[i] = re[j]; re[j] = t;
            t = im[i]; im[i] = im[j]; im[j] = t;
        }
    }
    for (int len = 2; len <= n; len <<= 1) {
        double a = -2 * M_PI / len, wr = cos(a), wi = sin(a);
        for (int i = 0; i < n; i += len) {
            double cr = 1, ci = 0;
            for (int k = 0; k < len / 2; k++) {
                int p = i + k, q = p + len / 2;
                double tr = re[q] * cr - im[q] * ci, ti = re[q] * ci + im[q] * cr;
                re[q] = re[p] - tr; im[q] = im[p] - ti;
                re[p] += tr; im[p] += ti;
                double nr = cr * wr - ci * wi;
                ci = cr * wi + ci * wr;
                cr = nr;
            }
        }
    }
}

static void fbank_frame(const short *x, float *out)
{
    static double re[VP_NFFT], im[VP_NFFT];
    double mean = 0;
    for (int i = 0; i < VP_FL; i++)
        mean += x[i];
    mean /= VP_FL;
    double prev = x[0] - mean;
    for (int i = 0; i < VP_FL; i++) {
        double v = x[i] - mean, pe = v - 0.97 * (i ? prev : v);
        prev = v;
        re[i] = pe * win[i];
        im[i] = 0;
    }
    memset(re + VP_FL, 0, sizeof(double) * (VP_NFFT - VP_FL));
    memset(im + VP_FL, 0, sizeof(double) * (VP_NFFT - VP_FL));
    fft(re, im, VP_NFFT);
    for (int m = 0; m < VP_NMEL; m++) {
        double e = 0;
        for (int k = bank_lo[m]; k <= bank_hi[m]; k++)
            e += banks[m][k] * (re[k] * re[k] + im[k] * im[k]);
        out[m] = (float)log(e > 1.1920929e-07 ? e : 1.1920929e-07);
    }
}

/* --- one utterance --- */

static struct {
    TfLiteInterpreter *body, *head;
    int head_n;            /* rows the head's input is sized for */
    short pcm[VP_FL];      /* samples not yet consumed by a frame */
    int npcm;
    float feats[VP_BLOCK][VP_NMEL];
    int nfeat;
    float rows[VP_HEAD][VP_ROW]; /* ring of the last VP_HEAD body rows */
    long nrows;                  /* rows ever produced in this utterance */
} vp;

static void vp_reset(void) { vp.npcm = vp.nfeat = 0, vp.nrows = 0; }

static void vp_feed(const short *s, size_t n)
{
    while (n > 0) {
        size_t k = VP_FL - vp.npcm;
        if (k > n)
            k = n;
        memcpy(vp.pcm + vp.npcm, s, k * sizeof(short));
        vp.npcm += k, s += k, n -= k;
        if (vp.npcm < VP_FL)
            break;
        fbank_frame(vp.pcm, vp.feats[vp.nfeat++]);
        memmove(vp.pcm, vp.pcm + VP_FS, (VP_FL - VP_FS) * sizeof(short));
        vp.npcm = VP_FL - VP_FS;
        if (vp.nfeat == VP_BLOCK) {
            float out[VP_BLOCK][VP_ROW];
            if (tfl_run(vp.body, &vp.feats[0][0], VP_BLOCK * VP_NMEL, &out[0][0], VP_BLOCK * VP_ROW) == 0)
                for (int i = 0; i < VP_BLOCK; i++)
                    memcpy(vp.rows[vp.nrows++ % VP_HEAD], out[i], sizeof out[i]);
            else
                fprintf(stderr, "voice: body failed\n");
            vp.nfeat = 0;
        }
    }
}

static void vp_print(const char *tag)
{
    double secs = (vp.nrows * VP_FS) / (double)VP_RATE;
    if (vp.nrows < VP_BLOCK) {
        printf("VOICE %s %.2f short\n", tag, secs);
        return;
    }
    int n = vp.nrows < VP_HEAD ? (int)vp.nrows : VP_HEAD;
    static float in[VP_HEAD][VP_ROW];
    for (int i = 0; i < n; i++) /* oldest first */
        memcpy(in[i], vp.rows[(vp.nrows - n + i) % VP_HEAD], sizeof in[i]);
    if (n != vp.head_n) {
        int dims[3] = {1, n, VP_ROW};
        if (tfl.resize(vp.head, 0, dims, 3) != 0 || tfl.alloc(vp.head) != 0) {
            printf("ERR head resize %d\n", n);
            return;
        }
        vp.head_n = n;
    }
    float emb[VP_DIM];
    if (tfl_run(vp.head, &in[0][0], (size_t)n * VP_ROW, emb, VP_DIM) != 0) {
        printf("ERR head failed\n");
        return;
    }
    static char line[VP_DIM * 12 + 128];
    int o = snprintf(line, sizeof line, "VOICE %s %.2f", tag, secs);
    for (int i = 0; i < VP_DIM; i++)
        o += snprintf(line + o, sizeof line - o, " %.5g", emb[i]);
    puts(line);
}

/* The frontend's compatibility tag from its config.json ("" if unreadable). */
static void read_tag(const char *dir, char *tag, size_t n)
{
    char p[1024], buf[4096];
    snprintf(p, sizeof p, "%s/tflite-online-ecapa-tdnn-frontend/config.json", dir);
    tag[0] = 0;
    FILE *f = fopen(p, "r");
    if (!f)
        return;
    size_t k = fread(buf, 1, sizeof buf - 1, f);
    fclose(f);
    buf[k] = 0;
    char *s = strstr(buf, "\"compatibility-tag\"");
    if (s && (s = strchr(s + 19, '"')) && sscanf(s + 1, "%127[^\"]", tag) != 1)
        tag[0] = 0;
    (void)n;
}

int cmd_voice(const char *dir)
{
    if (tfl_load() != 0)
        return 1;
    fbank_init();
    vp.body = tfl_open(dir, "tflite-online-ecapa-tdnn-frontend/body.tflite", "next");
    vp.head = tfl_open(dir, "tflite-online-ecapa-tdnn-frontend/head.tflite", "next");
    if (!vp.body || !vp.head)
        return 1;
    vp.head_n = 1;
    char tag[128];
    read_tag(dir, tag, sizeof tag);
    printf("READY %s\n", tag[0] ? tag : "unknown");
    fflush(stdout);

    char cmd[256];
    static short buf[VP_RATE]; /* 1 s */
    while (fgets(cmd, sizeof cmd, stdin)) {
        if (cmd[0] == 'a') {
            long n = atol(cmd + 1);
            while (n > 0) {
                size_t want = n > (long)sizeof buf ? sizeof buf : (size_t)n;
                if (fread(buf, 1, want, stdin) != want)
                    return 0;
                vp_feed(buf, want / 2);
                n -= want;
            }
        } else if (cmd[0] == 'v') {
            char *t = cmd + 1;
            while (*t == ' ')
                t++;
            t[strcspn(t, "\r\n")] = 0;
            vp_print(*t ? t : "-");
        } else if (cmd[0] == 'r') {
            vp_reset();
        } else {
            printf("ERR unknown command\n");
        }
        fflush(stdout);
    }
    return 0;
}
