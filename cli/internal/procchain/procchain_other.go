//go:build !darwin && !linux && !windows

package procchain

import "errors"

func lookup(pid int) (Proc, error) {
	return Proc{}, errors.New("procchain: no process table on this OS")
}
