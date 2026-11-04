#!/bin/sh
# Builds the universal odhelper binary on a Mac with the Xcode command line tools.
# The result (odhelper) is committed and embedded into cs-ldapclient (install_darwin.go).
cd "$(dirname "$0")" || exit 1
clang -fobjc-arc -arch x86_64 -arch arm64 -mmacosx-version-min=10.13 \
  -framework Foundation -framework OpenDirectory -framework SecurityFoundation \
  odhelper.m -o odhelper || exit 1
codesign -s - -f odhelper 2>/dev/null
file odhelper
