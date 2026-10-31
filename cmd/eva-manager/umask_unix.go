//go:build unix

package main

import "syscall"

// База, конфиг Xray и ключи доступны только пользователю панели (0600):
// SQLite создаёт файлы с правами по umask, поэтому сужаем её сразу.
func init() { syscall.Umask(0o077) }
