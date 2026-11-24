package stdio

import "syscall"

func statAtime(st *syscall.Stat_t) (int64, int64) { return st.Atimespec.Unix() }
func statCtime(st *syscall.Stat_t) (int64, int64) { return st.Ctimespec.Unix() }
func statDevice(st *syscall.Stat_t) uint64        { return uint64(uint32(st.Dev)) }
