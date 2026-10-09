// Package loggo suppresses the upstream CLI logger. Client diagnostics are
// provided by the core; this transport must not create files or print traffic.
package loggo

func Debug(string, ...any) {}
func Info(string, ...any)  {}
func Error(string, ...any) {}
