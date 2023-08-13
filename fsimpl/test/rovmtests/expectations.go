package rovmtests

type Dir struct {
	Path    string
	Members []string
}

type File struct {
	Path    string
	Content string
}

type Symlink struct {
	Path   string
	Target string
}

type Expectations struct {
	Dirs     []Dir
	Files    []File
	Symlinks []Symlink
}
