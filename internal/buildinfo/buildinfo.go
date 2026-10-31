// Package buildinfo хранит версию сборки. Version подставляется при сборке:
// go build -ldflags "-X github.com/lyovkindanil/Eva_Manager/internal/buildinfo.Version=0.1.0".
package buildinfo

// Version — версия панели; "dev" у сборки без ldflags.
var Version = "dev"
