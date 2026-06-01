package main

import "io"

// newMultiWriter 把日志同时写到多个 writer（stdout + 文件）。
func newMultiWriter(ws ...io.Writer) io.Writer {
	return io.MultiWriter(ws...)
}
