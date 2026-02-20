package utils

import "path/filepath"

// Directories stores directory paths for all directories used by the Server.
type Directories struct {
	CWD       string
	Image     string
	Thumbnail string
	Video     string
	Tmp       string
	Upload    string
}

// SetupDirectories builds default directory paths relative to cwd.
func SetupDirectories(cwd string) Directories {
	return Directories{
		CWD:       cwd,
		Image:     filepath.Join(cwd, "public", "images"),
		Thumbnail: filepath.Join(cwd, "public", "images", "thumbnails"),
		Video:     filepath.Join(cwd, "public", "videos"),
		Tmp:       filepath.Join(cwd, "tmp"),
		Upload:    filepath.Join(cwd, "public", "upload"),
	}
}
