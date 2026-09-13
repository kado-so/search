package payload

import "golang.org/x/sys/windows"

func plain(p string) error {
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return err
	}
	a, err := windows.GetFileAttributes(u)
	if err != nil {
		return err
	}
	if a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrInvalid
	}
	return nil
}
