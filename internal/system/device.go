package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// TermuxProbe lê o aparelho: bateria pelo Termux:API, memória/processos pelo /proc e disco por statfs (TSD §8.10).
// No Android 13 o /proc/stat e o loadavg são negados (PoC V-10), então não há CPU global.
type TermuxProbe struct {
	BatteryCmd string        // padrão: termux-battery-status
	Timeout    time.Duration // padrão: 5 s (o comando depende do app Termux:API)
	ProcRoot   string        // padrão: /proc
}

func (p TermuxProbe) proc() string {
	if p.ProcRoot != "" {
		return p.ProcRoot
	}
	return "/proc"
}

var ErrUnavailable = errors.New("indisponível")

func (p TermuxProbe) Battery(ctx context.Context) (BatteryInfo, error) {
	cmd := p.BatteryCmd
	if cmd == "" {
		cmd = "termux-battery-status"
	}
	to := p.Timeout
	if to == 0 {
		to = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	out, err := exec.CommandContext(cctx, cmd).Output()
	if err != nil {
		return BatteryInfo{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return ParseBattery(out)
}

// ParseBattery interpreta o JSON do termux-battery-status.
func ParseBattery(b []byte) (BatteryInfo, error) {
	var j struct {
		Percentage  *float64 `json:"percentage"`
		Temperature *float64 `json:"temperature"`
		Plugged     string   `json:"plugged"`
		Health      string   `json:"health"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(b), &j); err != nil || j.Percentage == nil {
		return BatteryInfo{}, fmt.Errorf("%w: resposta inesperada do termux-battery-status", ErrUnavailable)
	}
	bi := BatteryInfo{Percent: int(*j.Percentage), Charging: j.Plugged != "" && j.Plugged != "UNPLUGGED", Health: j.Health}
	if j.Temperature != nil {
		bi.TempC = *j.Temperature
	}
	return bi, nil
}

func (p TermuxProbe) Memory(context.Context) (MemInfo, error) {
	b, err := os.ReadFile(filepath.Join(p.proc(), "meminfo"))
	if err != nil {
		return MemInfo{}, err
	}
	return ParseMeminfo(b)
}

func ParseMeminfo(b []byte) (MemInfo, error) {
	var m MemInfo
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			m.TotalKB = v
		case "MemAvailable:":
			m.AvailableKB = v
		}
	}
	if m.TotalKB == 0 {
		return m, errors.New("meminfo sem MemTotal")
	}
	return m, nil
}

func (TermuxProbe) Disk(_ context.Context, path string) (DiskInfo, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return DiskInfo{}, err
	}
	bs := uint64(st.Bsize)
	return DiskInfo{TotalBytes: st.Blocks * bs, FreeBytes: st.Bavail * bs}, nil
}

// statFields extrai (ppid, utime+stime) de /proc/<pid>/stat. O nome do processo pode ter espaços e parênteses,
// então tudo é lido depois do ÚLTIMO ')'.
func statFields(b []byte) (ppid int, ticks uint64, ok bool) {
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0, 0, false
	}
	f := strings.Fields(string(b[i+1:])) // f[0]=state f[1]=ppid … f[11]=utime f[12]=stime
	if len(f) < 13 {
		return 0, 0, false
	}
	pp, err1 := strconv.Atoi(f[1])
	ut, err2 := strconv.ParseUint(f[11], 10, 64)
	st, err3 := strconv.ParseUint(f[12], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, false
	}
	return pp, ut + st, true
}

func vmRSS(b []byte) int64 {
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			f := strings.Fields(l)
			if len(f) >= 2 {
				v, _ := strconv.ParseInt(f[1], 10, 64)
				return v
			}
		}
	}
	return 0
}

// ProcessStats soma RSS e ticks de CPU de rootPID e de todos os descendentes.
func (p TermuxProbe) ProcessStats(_ context.Context, rootPID int) (ProcStats, error) {
	if rootPID <= 0 {
		return ProcStats{}, errors.New("pid inválido")
	}
	ents, err := os.ReadDir(p.proc())
	if err != nil {
		return ProcStats{}, err
	}
	children := map[int][]int{}
	ticks := map[int]uint64{}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(p.proc(), e.Name(), "stat"))
		if err != nil {
			continue // o processo pode ter morrido entre o ReadDir e a leitura
		}
		if pp, t, ok := statFields(b); ok {
			children[pp] = append(children[pp], pid)
			ticks[pid] = t
		}
	}
	if _, ok := ticks[rootPID]; !ok {
		return ProcStats{}, fmt.Errorf("processo %d não encontrado", rootPID)
	}
	var st ProcStats
	queue := []int{rootPID}
	seen := map[int]bool{rootPID: true}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		st.Procs++
		st.CPUTicks += ticks[pid]
		if b, err := os.ReadFile(filepath.Join(p.proc(), strconv.Itoa(pid), "status")); err == nil {
			st.RSSKB += vmRSS(b)
		}
		for _, c := range children[pid] {
			if !seen[c] {
				seen[c] = true
				queue = append(queue, c)
			}
		}
	}
	return st, nil
}

// FakeProbe devolve valores fixos (testes).
type FakeProbe struct {
	Bat    BatteryInfo
	BatErr error
	Mem    MemInfo
	Dsk    DiskInfo
	Procs  map[int]ProcStats
}

func (f *FakeProbe) Battery(context.Context) (BatteryInfo, error)   { return f.Bat, f.BatErr }
func (f *FakeProbe) Memory(context.Context) (MemInfo, error)        { return f.Mem, nil }
func (f *FakeProbe) Disk(context.Context, string) (DiskInfo, error) { return f.Dsk, nil }
func (f *FakeProbe) ProcessStats(_ context.Context, pid int) (ProcStats, error) {
	if s, ok := f.Procs[pid]; ok {
		return s, nil
	}
	return ProcStats{}, fmt.Errorf("processo %d não encontrado", pid)
}
