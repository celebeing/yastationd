/* Yandex SpeechKit embedded spotter C API, as exported by libyandex_evil.so.
 *
 * Reconstructed from the shim disassembly (see docs/spotter-decompile.md §2.2 and
 * spotter-host/README.md). Every call takes a trailing `spotter_error **err`;
 * on failure the call returns 0/NULL and stores an error object there.
 * Everything below was exercised under qemu-arm against the real library unless marked (?).
 */
#ifndef SPOTTER_API_H
#define SPOTTER_API_H

#include <stddef.h>

typedef struct spotter_error spotter_error;
typedef struct spotter_conf spotter_conf;
typedef struct spotter spotter;

enum spotter_event {
    SPOTTER_NEED_MORE_DATA_FOR_1ST_STAGE = 0,
    SPOTTER_SUBHIT = 1,
    SPOTTER_CONFIRMATION_1ST_STAGE = 2,
    SPOTTER_1ST_STAGE_HIT = 3,
    SPOTTER_NEED_MORE_DATA_FOR_2ND_STAGE = 4,
    SPOTTER_CONFIRMATION_2ND_STAGE = 5,
    SPOTTER_REJECTION_2ND_STAGE = 6,
    SPOTTER_FINISH = 7,
    SPOTTER_ACTIVATION_SEAMLESS = 8,
    SPOTTER_NEED_MORE_DATA_FOR_SEAM_VAD = 9,
};

/* Errors. Present in the 0.369 blob (bergamot/pickle), dropped from the 3.21 one (orion), so they
 * are weak references: NULL where the library lacks them (see report() in spotter_host.c). */
__attribute__((weak)) const char *spotter_error_get_message(const spotter_error *e);
__attribute__((weak)) const char *spotter_error_get_func_name(const spotter_error *e);
__attribute__((weak)) int spotter_error_get_type(const spotter_error *e);
__attribute__((weak)) void spotter_error_free(spotter_error **e); /* takes the slot, not the object (observed) */

/* Model configuration: a model directory with flags.txt, words.txt, acoustic_model.nnet. */
spotter_conf *spotter_conf_read(const char *dir, spotter_error **err);
void spotter_conf_free(spotter_conf *conf, spotter_error **err);
int spotter_conf_native_sample_rate(const spotter_conf *conf, spotter_error **err);
/* Number of input channels of the given channel type the model requires. */
int spotter_conf_get_required_channel_count(const spotter_conf *conf, int chan_type,
                                            spotter_error **err);

/* `sample_rate` must be a multiple of the native rate (16000);
 * `opt`: on the pickle (aarch64) build this is a bool (`and w2,#1`; in the multi-conf path it
 * looks like a parallel-runner switch). Pass 0/NULL — kept as a pointer-sized slot so the armhf
 * 0.369 call is unchanged. */
spotter *spotter_create(const spotter_conf *conf, int sample_rate, void *opt, spotter_error **err);
/* `chan_types[n]`: channel type per interleaved input channel. Accepted values are 0..5:
 *   0 = omni (VQE output, vqe_0), 3 = BNR (vqe_bnr), 4 = raw mic (freq-filter input);
 *   1, 2, 5 are probably beamforming, feedback and extra raw mics (?).
 * The library checks the per-type counts against spotter_conf_get_required_channel_count.
 * spotter_create() is the same call with the fixed layout {0}. `opt` as in spotter_create. */
spotter *spotter_create_multichannel(const spotter_conf *conf, int sample_rate, int n,
                                     const int *chan_types, void *opt, spotter_error **err);
void spotter_destroy(spotter *s, spotter_error **err);

/* Interleaved S16 PCM; nbytes must be a multiple of 2 * channels. */
int spotter_submit_raw_data(spotter *s, const void *pcm, size_t nbytes, spotter_error **err);
int spotter_get_next_event(spotter *s, spotter_error **err);
const char *spotter_get_current_phrase(spotter *s, spotter_error **err);

#endif
