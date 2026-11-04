package awg

import "syscall"

// sysProcAttr — своя группа процессов (Ctrl+C в терминале получает только
// панель, и она останавливает интерфейсы по порядку) и SIGTERM процессу,
// если панель умерла без уборки.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
