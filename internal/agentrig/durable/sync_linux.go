package durable

import "os"

func syncData(f *os.File) error { return f.Sync() }
