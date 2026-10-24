package host

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCaptureStatus(t *testing.T) {
	active, err := os.ReadFile(filepath.Join("testdata", "pcap-status-on.txt"))
	if err != nil {
		t.Fatal(err)
	}
	status := parseCaptureStatus(string(active))
	if !status.Active || status.Interface != "vm-01" || status.File != "rookery-test.pcap" {
		t.Fatalf("active status = %+v", status)
	}
	if status.Direction != "both" || status.MaxPackets != 5000 || status.Buffered != 21 {
		t.Fatalf("active details = %+v", status)
	}

	dispatch := parseCaptureStatus("pcap rx and tx dispatch capture enabled: 2 of 820 pkts...\ncapture to file /tmp/rookery-dispatch.pcap\n")
	if !dispatch.Active || dispatch.Direction != "both" || dispatch.Buffered != 2 || dispatch.File != "rookery-dispatch.pcap" {
		t.Fatalf("dispatch status = %+v", dispatch)
	}

	off := parseCaptureStatus("max is 5000, for interface vm-01 to file /tmp/rookery-test.pcap\npcap rx/tx capture is off...\ncaptured 42 pkts...\nsaved to /tmp/rookery-test.pcap...\n")
	if off.Active || off.Packets != 42 || off.File != "rookery-test.pcap" {
		t.Fatalf("off status = %+v", off)
	}

	writeOff := parseCaptureStatus("Write 2 packets to /tmp/rookery-write.pcap, and stop capture...\n")
	if writeOff.Active || writeOff.Packets != 2 || writeOff.File != "rookery-write.pcap" {
		t.Fatalf("write off status = %+v", writeOff)
	}
}

func TestListAndOpenPacketCaptures(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"rookery-a.pcap":     "one",
		"rookery-b.pcapng":   "two",
		"other-capture.pcap": "ignored",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	v := &VPPNetwork{CaptureDir: dir}
	captures, err := v.ListPacketCaptures()
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 2 {
		t.Fatalf("captures = %+v", captures)
	}
	for _, capture := range captures {
		if capture.Name == "other-capture.pcap" {
			t.Fatalf("listed unrelated capture: %+v", captures)
		}
	}
	file, info, err := v.OpenPacketCapture("rookery-a.pcap")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if info.Name != "rookery-a.pcap" || info.Size != 3 {
		t.Fatalf("info = %+v", info)
	}
	if _, _, err := v.OpenPacketCapture("../rookery-a.pcap"); !errors.Is(err, ErrCaptureNotFound) {
		t.Fatalf("traversal error = %v", err)
	}
	if _, _, err := v.OpenPacketCapture("other-capture.pcap"); !errors.Is(err, ErrCaptureNotFound) {
		t.Fatalf("unrelated error = %v", err)
	}

	v.capture = &PacketCaptureStatus{Active: true, File: "rookery-b.pcapng"}
	if err := v.DeletePacketCapture("rookery-b.pcapng"); !errors.Is(err, ErrCaptureFileActive) {
		t.Fatalf("active delete error = %v", err)
	}
	if err := v.DeletePacketCapture("rookery-a.pcap"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := v.OpenPacketCapture("rookery-a.pcap"); !errors.Is(err, ErrCaptureNotFound) {
		t.Fatalf("deleted file error = %v", err)
	}
	if err := v.DeletePacketCapture("../rookery-a.pcap"); !errors.Is(err, ErrCaptureNotFound) {
		t.Fatalf("delete traversal error = %v", err)
	}
}

func TestPacketCaptureCLIStartStop(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := filepath.Join(dir, "fake-vppctl")
	contents := `#!/bin/sh
if [ "$3" = "status" ]; then
  if [ -f "$CAP_DIR/name" ]; then
    name=$(cat "$CAP_DIR/name")
    echo "max is 509, for interface vm-01 to file /tmp/$name"
    echo "pcap rx/tx capture is on: 0 of 509 pkts..."
  else
    echo "pcap dispatch capture disabled"
  fi
  exit 0
fi
if [ "$3" = "off" ]; then
  name=$(cat "$CAP_DIR/name")
  printf 'pcap' > "$CAP_DIR/$name"
  echo "captured 4 pkts..."
  echo "saved to /tmp/$name..."
  exit 0
fi
echo "$@" > "$ARGS_FILE"
previous=""
for argument in "$@"; do
  if [ "$previous" = "file" ]; then
    printf '%s' "$argument" > "$CAP_DIR/name"
  fi
  previous="$argument"
done
name=$(cat "$CAP_DIR/name")
printf 'pcap' > "$CAP_DIR/$name"
echo "pcap rx/tx capture is on: 0 of 509 pkts..."
`
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAP_DIR", dir)
	t.Setenv("ARGS_FILE", argsFile)
	v := &VPPNetwork{CLI: script, CaptureDir: dir}
	status, err := v.StartPacketCapture(t.Context(), PacketCaptureOptions{
		Interface:  "vm-01",
		Direction:  "both",
		MaxBytes:   1024 * 1024,
		SnapLength: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Active || status.MaxPackets != 509 || status.Interface != "vm-01" {
		t.Fatalf("start status = %+v", status)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	argsText := string(args)
	if !strings.Contains(argsText, "pcap trace rx tx max 509 intfc vm-01 file rookery-") ||
		!strings.Contains(argsText, "max-bytes-per-pkt 2048") {
		t.Fatalf("capture args = %q", argsText)
	}
	stopped, err := v.StopPacketCapture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Active || stopped.Packets != 4 || stopped.Size != 4 || stopped.File == "" {
		t.Fatalf("stop status = %+v", stopped)
	}
	captures, err := v.ListPacketCaptures()
	if err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1 || captures[0].Name != stopped.File {
		t.Fatalf("captures = %+v, stop = %+v", captures, stopped)
	}
}
