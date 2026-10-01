package schema

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed migrations/*.sql
var files embed.FS

func MigrationNames() ([]string, error) {
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && len(entry.Name()) > 4 && entry.Name()[len(entry.Name())-4:] == ".sql" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func Migration(name string) ([]byte, error) {
	body, err := files.ReadFile("migrations/" + name)
	if err != nil {
		return nil, fmt.Errorf("read migration %q: %w", name, err)
	}
	return body, nil
}
