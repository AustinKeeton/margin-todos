//go:build !linux

package main

import "errors"

func watch(*store) error { return errors.New("watching needs Linux (inotify); use -once here") }
