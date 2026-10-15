package analysis

import (
	"os"
	"sync"
	"time"
)

// diskTexts holds the text of each project file a program check read from
// disk, by absolute path, with the file's stat when it was read, so that
// the next analysis of a kept program reads again only the files whose stat
// changed (readProjectFile). Each analysis of an open document compares
// every closed file of its program with the text the program was checked
// from; reading 221 files took 3.5 ms of an 8.5 ms body edit at 50k lines.
type diskTexts struct {
	mu    sync.Mutex
	files map[string]diskText
}

// diskText is a file's text as read, and its stat from just before.
type diskText struct {
	stamp diskStamp
	text  string
	// racy reports that the file's modification time was within racyWindow
	// of the clock when it was stat'ed: a later write in the same timestamp
	// tick, with the same size, would leave the stat unchanged.
	racy bool
}

// diskStamp is what a stat says of a file that changes when it is written:
// its size, its modification time and its inode (0 where the platform has
// none, inodeOf). Replacing a file with another (a rename over it, as
// editors and git save) changes the inode even when the rest agrees.
type diskStamp struct {
	size  int64
	mtime int64
	inode uint64
}

// racyWindow is how close to the clock a modification time must be for
// the stat not to be trusted, as Go's build cache does (cmd/go's
// modTimeCutoff) and as git does for an index entry no older than the index
// (Documentation/technical/racy-git.txt). Filesystems keep modification
// times at a coarse tick (a second or two on some), so a file written twice
// within one tick with the same size keeps its stat. A racy file is read and
// compared on every analysis until a read finds its modification time
// older than the window, and then its stat is trusted.
const racyWindow = 2 * time.Second

// stampOf is info's diskStamp.
func stampOf(info os.FileInfo) diskStamp {
	return diskStamp{size: info.Size(), mtime: info.ModTime().UnixNano(), inode: inodeOf(info)}
}

// readProjectFile answers the text of the file at path: the text read last
// time when the file's stat is what it was then and was not racy, and
// otherwise the file read afresh. The stat is taken before the read, so a
// write between them leaves a stat older than the text, and the next call
// reads the file again.
func (dm *DocumentManager) readProjectFile(path string) (string, error) {
	now := time.Now()
	info, err := os.Stat(path)
	if err != nil {
		dm.disk.forget(path)
		return "", err
	}
	stamp := stampOf(info)
	d := &dm.disk
	d.mu.Lock()
	kept, ok := d.files[path]
	d.mu.Unlock()
	if ok && !kept.racy && kept.stamp == stamp {
		return kept.text, nil
	}
	noteDiskRead(path)
	data, err := os.ReadFile(path)
	if err != nil {
		dm.disk.forget(path)
		return "", err
	}
	text := string(data)
	if ok && kept.text == text {
		// The same text: keep the string the programs hold, so comparing
		// them again finds them equal at once.
		text = kept.text
	}
	d.mu.Lock()
	if d.files == nil {
		d.files = map[string]diskText{}
	}
	d.files[path] = diskText{stamp: stamp, text: text, racy: info.ModTime().After(now.Add(-racyWindow))}
	d.mu.Unlock()
	return text, nil
}

// forget drops what d holds of path.
func (d *diskTexts) forget(path string) {
	d.mu.Lock()
	delete(d.files, path)
	d.mu.Unlock()
}
