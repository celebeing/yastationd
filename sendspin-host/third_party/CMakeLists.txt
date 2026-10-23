# Builds sendspin-cpp and its dependencies as static libraries for sendspin-host.
# Sources come from third_party/src/ (fetch.sh); nothing is downloaded here. Driven by ../Makefile.
# Adapted from pulsar's satellite/third_party/CMakeLists.txt (MIT, (c) 2026 pulsar contributors).
cmake_minimum_required(VERSION 3.16)
project(sendspin_host_deps C CXX)

set(_src ${CMAKE_CURRENT_SOURCE_DIR}/src)
set(FETCHCONTENT_FULLY_DISCONNECTED ON CACHE BOOL "" FORCE)
set(FETCHCONTENT_SOURCE_DIR_ARDUINOJSON ${_src}/ArduinoJson CACHE PATH "" FORCE)
set(FETCHCONTENT_SOURCE_DIR_MICRO_FLAC ${_src}/micro-flac CACHE PATH "" FORCE)
set(FETCHCONTENT_SOURCE_DIR_IXWEBSOCKET ${_src}/IXWebSocket CACHE PATH "" FORCE)
set(FETCHCONTENT_SOURCE_DIR_NOISE_C ${_src}/noise-c CACHE PATH "" FORCE)

# Player only, PCM + FLAC (no Opus: not worth the CPU on these SoCs, and MA sends FLAC/PCM on a LAN).
# Keep in sync with SS_DEFS in ../Makefile.
set(SENDSPIN_ENABLE_PLAYER ON CACHE BOOL "" FORCE)
set(SENDSPIN_ENABLE_OPUS OFF CACHE BOOL "" FORCE)
set(SENDSPIN_ENABLE_METADATA OFF CACHE BOOL "" FORCE)
set(SENDSPIN_ENABLE_CONTROLLER OFF CACHE BOOL "" FORCE)
set(SENDSPIN_ENABLE_COLOR OFF CACHE BOOL "" FORCE)
set(SENDSPIN_ENABLE_ARTWORK OFF CACHE BOOL "" FORCE)
set(SENDSPIN_ENABLE_VISUALIZER OFF CACHE BOOL "" FORCE)
set(SENDSPIN_ENABLE_SOURCE OFF CACHE BOOL "" FORCE)
set(BUILD_EXAMPLES OFF CACHE BOOL "" FORCE)
set(SENDSPIN_USE_CCACHE OFF CACHE BOOL "" FORCE)
set(MICRO_FLAC_ENABLE_OGG OFF CACHE BOOL "" FORCE)
set(USE_TLS OFF CACHE BOOL "" FORCE)
set(USE_ZLIB OFF CACHE BOOL "" FORCE)
set(IXWEBSOCKET_INSTALL OFF CACHE BOOL "" FORCE)

add_subdirectory(${_src}/sendspin-cpp sendspin)
