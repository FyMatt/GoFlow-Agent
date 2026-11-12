package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/persistence"
)

func runDataMaintenance(runtimeHome string, args []string, output io.Writer) (bool, error) {
	action := ""
	archive := ""
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--backup-data":
			if action != "" {
				return true, fmt.Errorf("choose only one data maintenance action")
			}
			action = "backup"
		case "--restore-data":
			if action != "" {
				return true, fmt.Errorf("choose only one data maintenance action")
			}
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return true, fmt.Errorf("--restore-data requires a backup path or file name")
			}
			action = "restore"
			archive = args[index+1]
			index++
		}
	}
	if action == "" {
		return false, nil
	}
	dataRoot := config.ResolveDataRoot(runtimeHome)
	store := persistence.New(dataRoot)
	switch action {
	case "backup":
		path, err := store.CreateBackup()
		if err != nil {
			return true, err
		}
		_, err = fmt.Fprintf(output, "Data backup created: %s\n", path)
		return true, err
	case "restore":
		if !filepath.IsAbs(archive) {
			if filepath.Base(archive) != archive {
				return true, fmt.Errorf("relative backup must be a file name from the backups directory")
			}
			archive = filepath.Join(dataRoot, "backups", archive)
		}
		if err := store.RestoreBackup(archive); err != nil {
			return true, err
		}
		_, err := fmt.Fprintf(output, "Data backup restored. Restart GoFlow normally: %s\n", archive)
		return true, err
	default:
		return true, fmt.Errorf("unsupported data maintenance action")
	}
}
