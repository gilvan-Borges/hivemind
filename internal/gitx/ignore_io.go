package gitx

import "os"

func osRead(path string) ([]byte, error) { return os.ReadFile(path) }

func osWrite(path string, data []byte) error { return os.WriteFile(path, data, 0o644) }

func osNotExist(err error) bool { return os.IsNotExist(err) }
