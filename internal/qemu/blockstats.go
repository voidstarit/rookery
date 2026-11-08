package qemu

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// BlockDeviceStats reports cumulative QEMU block-layer counters for one drive.
type BlockDeviceStats struct {
	Device          string
	ReadBytes       uint64
	WriteBytes      uint64
	ReadOperations  uint64
	WriteOperations uint64
	FailedReadOps   uint64
	FailedWriteOps  uint64
	InvalidReadOps  uint64
	InvalidWriteOps uint64
}

type qmpStats struct {
	ReadBytes       uint64 `json:"rd_bytes"`
	WriteBytes      uint64 `json:"wr_bytes"`
	ReadOperations  uint64 `json:"rd_operations"`
	WriteOperations uint64 `json:"wr_operations"`
	FailedReadOps   uint64 `json:"failed_rd_operations"`
	FailedWriteOps  uint64 `json:"failed_wr_operations"`
	InvalidReadOps  uint64 `json:"invalid_rd_operations"`
	InvalidWriteOps uint64 `json:"invalid_wr_operations"`
}

type qmpBlockStats struct {
	Device   string    `json:"device"`
	NodeName string    `json:"node-name"`
	Stats    *qmpStats `json:"stats"`
}

// BlockStats returns per-drive QEMU block statistics from QMP.
func (q *QEMU) BlockStats(ctx context.Context, vmID string) ([]BlockDeviceStats, error) {
	if !q.Running(vmID) {
		return nil, nil
	}
	vnc, _, _, err := q.paths(vmID)
	if err != nil {
		return nil, err
	}
	socket := qmpPathFromSocket(vnc)
	dialer := net.Dialer{Timeout: monitorTimeout}
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %v", ErrMonitorUnavailable, err)
		}
		return nil, fmt.Errorf("connect qemu qmp: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(monitorTimeout)
	if deadlineAt, ok := ctx.Deadline(); ok && deadlineAt.Before(deadline) {
		deadline = deadlineAt
	}
	_ = conn.SetDeadline(deadline)
	reader := bufio.NewReader(conn)
	// QMP sends a greeting before the first command.
	if _, err := reader.ReadString('\n'); err != nil {
		return nil, fmt.Errorf("read qemu qmp greeting: %w", err)
	}
	if err := qmpCommand(conn, reader, "qmp_capabilities", nil); err != nil {
		return nil, err
	}
	var entries []qmpBlockStats
	if err := qmpCommand(conn, reader, "query-blockstats", &entries); err != nil {
		return nil, err
	}
	stats := make([]BlockDeviceStats, 0, len(entries))
	for _, entry := range entries {
		if entry.Stats == nil {
			continue
		}
		device := strings.TrimSpace(entry.Device)
		if device == "" {
			device = strings.TrimSpace(entry.NodeName)
		}
		stats = append(stats, BlockDeviceStats{
			Device:    device,
			ReadBytes: entry.Stats.ReadBytes, WriteBytes: entry.Stats.WriteBytes,
			ReadOperations: entry.Stats.ReadOperations, WriteOperations: entry.Stats.WriteOperations,
			FailedReadOps: entry.Stats.FailedReadOps, FailedWriteOps: entry.Stats.FailedWriteOps,
			InvalidReadOps: entry.Stats.InvalidReadOps, InvalidWriteOps: entry.Stats.InvalidWriteOps,
		})
	}
	return stats, nil
}

func qmpCommand(conn net.Conn, reader *bufio.Reader, command string, result any) error {
	payload := map[string]any{"execute": command}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write qemu qmp command: %w", err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read qemu qmp response: %w", err)
		}
		var envelope struct {
			Return json.RawMessage `json:"return"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}
		if len(envelope.Error) > 0 {
			return fmt.Errorf("qemu qmp %s: %s", command, string(envelope.Error))
		}
		if len(envelope.Return) == 0 {
			continue
		}
		if result != nil {
			if err := json.Unmarshal(envelope.Return, result); err != nil {
				return fmt.Errorf("decode qemu qmp %s: %w", command, err)
			}
		}
		return nil
	}
}
