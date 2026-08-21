/* Hand-written configuration for the vendored libopus float build:
   no intrinsics, no RTCD, no ML features (equivalent of configuring with
   --disable-intrinsics --disable-rtcd; dnn/ is not vendored at all).
   Rationale and measurements: docs/research/alternatives/opus.md. */
#ifndef GUL_OPUS_CONFIG_H
#define GUL_OPUS_CONFIG_H

#define OPUS_BUILD 1
#define PACKAGE_VERSION "1.6.1-gul"
#define VAR_ARRAYS 1
#define HAVE_LRINTF 1
#define HAVE_LRINT 1
#define FLOAT_APPROX 1
#define ENABLE_HARDENING 1

#endif
