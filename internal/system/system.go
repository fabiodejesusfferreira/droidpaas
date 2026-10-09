// Package system reúne as fronteiras com o ambiente (TSD §8.10, §15.3):
// aparelho, relógio, processos e git. Reais e fakes chegam com cada sub-fase.
package system

import (
	"context"
	"io"
	"time"
)

type BatteryInfo struct {
	Percent  int
	TempC    float64
	Charging bool
	Health   string
}

type MemInfo struct{ TotalKB, AvailableKB int64 }

type DiskInfo struct{ TotalBytes, FreeBytes uint64 }

type ProcStats struct {
	RSSKB      int64
	CPUPercent float64
}

type DeviceProbe interface {
	Battery(ctx context.Context) (BatteryInfo, error)
	Memory(ctx context.Context) (MemInfo, error)
	Disk(ctx context.Context, path string) (DiskInfo, error)
	ProcessStats(ctx context.Context, rootPID int) (ProcStats, error)
}

type Clock interface {
	Now() time.Time
}

// CommandRunner executa sempre por vetor de argumentos, nunca via shell (TSD §8.3.2).
type CommandRunner interface {
	Run(ctx context.Context, dir string, env []string, out io.Writer, name string, args ...string) error
}

type GitRunner interface {
	// Archive extrai o conteúdo de sha do repo bare em dest (git archive | tar -x).
	Archive(ctx context.Context, repo, sha, dest string) error
	RevParse(ctx context.Context, repo, ref string) (string, error)
}
