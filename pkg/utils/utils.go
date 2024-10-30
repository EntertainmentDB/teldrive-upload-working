package utils

import (
	"os"
	"path/filepath"
)

func ExecutableDir() string {

	path, _ := os.Executable()

	return filepath.Dir(path)
}
