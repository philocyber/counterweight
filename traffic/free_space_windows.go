package traffic

import (
	"golang.org/x/sys/windows"
	"path/filepath"
)

func trafficAvailableBytes(dir string) (uint64, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return 0, err
	}
	path, err := windows.UTF16PtrFromString(absolute + string(filepath.Separator))
	if err != nil {
		return 0, err
	}
	var available uint64
	err = windows.GetDiskFreeSpaceEx(path, &available, nil, nil)
	return available, err
}
