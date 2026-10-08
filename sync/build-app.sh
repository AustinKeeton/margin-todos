#!/bin/bash
# Build MarginTodosSync.app (Apple Silicon, macOS 14+), ad-hoc signed.
set -e
cd "$(dirname "$0")"
APP=MarginTodosSync.app
rm -rf "$APP" && mkdir -p "$APP/Contents/MacOS"
cp Info.plist "$APP/Contents/Info.plist"
swiftc -O -target arm64-apple-macos14.0 -o "$APP/Contents/MacOS/MarginTodosSync" MarginTodosSync.swift
codesign --force --sign - "$APP"
codesign --verify "$APP" && echo "built $APP"
