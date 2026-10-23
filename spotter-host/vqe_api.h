/* Yandex VQE (mic-array front-end) C API, as exported by libyandex_evil.so.
 *
 * Reconstructed from the disassembly of the exported wrappers (addresses below are vaddrs in
 * libyandex_evil.so md5 2efb61a8…), see docs/voice-phase2-plan.md §VQE. Every call that can fail
 * returns a YandexVqeC_Error* (NULL on success); the wrappers catch C++ exceptions and turn them into
 * an error object. Things marked (?) are inferred, not confirmed by a run.
 */
#ifndef VQE_API_H
#define VQE_API_H

#include <stddef.h>

typedef struct YandexVqeC_Error YandexVqeC_Error;   /* 12 bytes: a libc++ std::string */
typedef struct YandexVqeC_Config YandexVqeC_Config; /* 0x910 bytes: vqe::Config + 3 words */
typedef struct YandexVqeC_Vqe YandexVqeC_Vqe;       /* 0x21b0 bytes */

/* Output channel types (index into the name table at .data.rel.ro 0x4b5188). */
enum vqe_channel_type {
    VQE_CH_RAW_MIC = 1,  /* 3 on bergamot */
    VQE_CH_RAW_SYNC_MIC_MAIN = 2,
    VQE_CH_RAW_SYNC_MIC_AUX = 3,
    VQE_CH_RAW_SPK = 4,  /* 1 on bergamot (HW feedback) */
    VQE_CH_RAW_SPK_SYNC = 5,
    VQE_CH_VQE = 6,      /* vqe_0: the AEC'd picked omni mic */
    VQE_CH_VQE_BF = 7,   /* 0 on bergamot */
    VQE_CH_VQE_BNR = 8,  /* vqe_bnr */
    VQE_CH_VQE_GAIN = 9, /* vqe_0 x omniChannelGainCoefficient */
};

const char *YandexVqeC_Error_GetMessage(const YandexVqeC_Error *e); /* 0x102f30 */
void YandexVqeC_Error_Free(YandexVqeC_Error *e);                   /* 0x102f48, takes the object */

/* 0x198d1c: look up a compiled-in preset ("bergamot") and return a heap copy. */
YandexVqeC_Error *YandexVqeC_Config_CreateFromPreset(const char *name, YandexVqeC_Config **out);
void YandexVqeC_Config_Free(YandexVqeC_Config *c);                  /* 0x199198 */
int YandexVqeC_Config_GetMicsCount(const YandexVqeC_Config *c);     /* 0x1993cc */
int YandexVqeC_Config_GetSpeakersCount(const YandexVqeC_Config *c); /* 0x1993ec */
int YandexVqeC_Config_GetInputSamplingRate(const YandexVqeC_Config *c);  /* +124 */
int YandexVqeC_Config_GetOutputSamplingRate(const YandexVqeC_Config *c); /* +132 */
int YandexVqeC_Config_GetProcessingFrame(const YandexVqeC_Config *c);    /* +136 */
/* in_rate / proc_rate * processing_frame: frames per channel per Process() call (768 on bergamot) */
int YandexVqeC_Config_GetInputChunkSize(const YandexVqeC_Config *c);
/* 0x199434: second arg is passed through to the per-type counter; (?) it's the channel type.
 * `out` is a size_t: the aarch64 (pickle) lib stores 8 bytes there (same width as int on armhf). */
YandexVqeC_Error *YandexVqeC_Config_GetOutputChannelCount(const YandexVqeC_Config *c, int type,
                                                          size_t *out);

/* 0x1994bc: model_dir = defaultModelStoragePath (e.g. /system/vendor/quasar/vqe_models);
 * n_models/models = optional {name, path} string pairs (?); NULL/0 works. */
typedef struct { const char *name, *path; } YandexVqeC_ModelPath;
YandexVqeC_Error *YandexVqeC_Create(const YandexVqeC_Config *c, const char *model_dir, size_t n_models,
                                    const YandexVqeC_ModelPath *models, YandexVqeC_Vqe **out);
void YandexVqeC_Free(YandexVqeC_Vqe *v); /* 0x199854 */

/* 0x199870 -> 0x1d34ec: one chunk. `mic` is interleaved float [chunk * mics], `spk` interleaved
 * float [chunk * speakers]; the counts are checked against the config and the call throws otherwise.
 * Sample scale: see the README (measured). */
YandexVqeC_Error *YandexVqeC_Process(YandexVqeC_Vqe *v, const float *mic, size_t mic_samples,
                                     const float *spk, size_t spk_samples);
/* 0x199900: pointer + length (in floats) of the latest chunk of output channel (type, index).
 * Pointer is owned by the VQE object and valid until the next Process(). */
YandexVqeC_Error *YandexVqeC_GetOutputChannelData(YandexVqeC_Vqe *v, int type, int index,
                                                  const float **data, size_t *n);
YandexVqeC_Error *YandexVqeC_SetASRMode(YandexVqeC_Vqe *v, int mode);           /* (?) semantics */
YandexVqeC_Error *YandexVqeC_SetCommunicationMode(YandexVqeC_Vqe *v, int mode); /* (?) */
YandexVqeC_Error *YandexVqeC_GetOmniMicIndex(YandexVqeC_Vqe *v, int *out);       /* 0x199a24 */
/* 0x199a44: one confidence per mic (float, (?)) */
YandexVqeC_Error *YandexVqeC_GetMicHealthConfidences(YandexVqeC_Vqe *v, const float **c, size_t *n);

#endif
