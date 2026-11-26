//go:build windows

package main

import "golang.org/x/sys/windows"

// 한글이 깨지지 않도록 콘솔을 UTF-8로.
func init() {
	windows.SetConsoleOutputCP(65001)
	windows.SetConsoleCP(65001)
}
