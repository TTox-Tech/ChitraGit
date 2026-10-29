package schedule

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
)

// schtasks manages a task in Task Scheduler's \ChitraGit\ folder. The task
// is defined in XML so long paths and arguments with spaces survive intact.
type schtasks struct{ env Env }

func (s *schtasks) Name() string { return "Task Scheduler" }

func taskName(id string) string { return `\ChitraGit\` + id }

func (s *schtasks) Install(ctx context.Context, spec Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	dir := s.env.TempDir
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "chitragit-task-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(utf16LE(taskXML(spec, time.Now())))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if _, errOut, err := s.env.Run(ctx, nil, "schtasks", "/Create", "/TN", taskName(spec.ID()), "/XML", f.Name(), "/F"); err != nil {
		return schedulerError("Task Scheduler refused the job", errOut, err)
	}
	return nil
}

func (s *schtasks) Installed(ctx context.Context, id string) (bool, error) {
	_, _, err := s.env.Run(ctx, nil, "schtasks", "/Query", "/TN", taskName(id))
	return err == nil, nil
}

func (s *schtasks) Remove(ctx context.Context, id string) error {
	if ok, _ := s.Installed(ctx, id); !ok {
		return nil
	}
	if _, errOut, err := s.env.Run(ctx, nil, "schtasks", "/Delete", "/TN", taskName(id), "/F"); err != nil {
		return schedulerError("cannot remove the scheduled task", errOut, err)
	}
	return nil
}

func taskXML(spec Spec, now time.Time) []byte {
	esc := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), spec.Hour, spec.Minute, 0, 0, time.Local).Format("2006-01-02T15:04:05")
	schedule := "<ScheduleByDay><DaysInterval>1</DaysInterval></ScheduleByDay>"
	if spec.Frequency == Weekly {
		schedule = fmt.Sprintf("<ScheduleByWeek><WeeksInterval>1</WeeksInterval><DaysOfWeek><%s /></DaysOfWeek></ScheduleByWeek>", spec.Weekday)
	}
	args := make([]string, 0, len(spec.Args()))
	for _, a := range spec.Args() {
		args = append(args, winQuote(a))
	}
	return []byte(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>ChitraGit: sync the archive at ` + esc(spec.Archive) + `</Description>
  </RegistrationInfo>
  <Triggers>
    <CalendarTrigger>
      <StartBoundary>` + start + `</StartBoundary>
      <Enabled>true</Enabled>
      ` + schedule + `
    </CalendarTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT72H</ExecutionTimeLimit>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + esc(spec.Binary) + `</Command>
      <Arguments>` + esc(strings.Join(args, " ")) + `</Arguments>
      <WorkingDirectory>` + esc(filepath.Clean(spec.Archive)) + `</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`)
}

// utf16LE encodes s with a byte-order mark, which schtasks /XML expects.
func utf16LE(b []byte) []byte {
	units := utf16.Encode([]rune(string(b)))
	out := make([]byte, 2, 2+2*len(units))
	out[0], out[1] = 0xFF, 0xFE
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// winQuote quotes one argument the way Windows programs split a command
// line (the CommandLineToArgvW rules).
func winQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			slashes++
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, 2*slashes+1))
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
		}
		slashes = 0
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat(`\`, 2*slashes))
	b.WriteByte('"')
	return b.String()
}
