package storage

import (
	"encoding/json"
	"fmt"
	"strings"
)

// smartctl -j output (smartmontools 7.x), the parts we use.
type smartJSON struct {
	Smartctl struct {
		ExitStatus int `json:"exit_status"`
		Messages   []struct {
			String   string `json:"string"`
			Severity string `json:"severity"`
		} `json:"messages"`
	} `json:"smartctl"`
	Device struct {
		Protocol string `json:"protocol"`
	} `json:"device"`
	SmartSupport *struct {
		Available bool `json:"available"`
		Enabled   bool `json:"enabled"`
	} `json:"smart_support"`
	SmartStatus *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current int64 `json:"current"`
	} `json:"temperature"`
	PowerOnTime *struct {
		Hours int64 `json:"hours"`
	} `json:"power_on_time"`
	ATASmartData *struct {
		SelfTest *struct {
			Status struct {
				Value            int64  `json:"value"`
				String           string `json:"string"`
				RemainingPercent *int64 `json:"remaining_percent"`
			} `json:"status"`
		} `json:"self_test"`
	} `json:"ata_smart_data"`
	ATAAttributes *struct {
		Table []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Value      int64  `json:"value"`
			Worst      int64  `json:"worst"`
			Thresh     int64  `json:"thresh"`
			WhenFailed string `json:"when_failed"`
			Raw        struct {
				Value  int64  `json:"value"`
				String string `json:"string"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	ATASelfTestLog *struct {
		Standard *struct {
			Table []struct {
				Type struct {
					String string `json:"string"`
				} `json:"type"`
				Status struct {
					String string `json:"string"`
				} `json:"status"`
				LifetimeHours int64 `json:"lifetime_hours"`
			} `json:"table"`
		} `json:"standard"`
	} `json:"ata_smart_self_test_log"`
	NVMeLog *struct {
		CriticalWarning  int64 `json:"critical_warning"`
		Temperature      int64 `json:"temperature"`
		AvailableSpare   int64 `json:"available_spare"`
		SpareThreshold   int64 `json:"available_spare_threshold"`
		PercentageUsed   int64 `json:"percentage_used"`
		DataUnitsRead    int64 `json:"data_units_read"`
		DataUnitsWritten int64 `json:"data_units_written"`
		PowerCycles      int64 `json:"power_cycles"`
		PowerOnHours     int64 `json:"power_on_hours"`
		UnsafeShutdowns  int64 `json:"unsafe_shutdowns"`
		MediaErrors      int64 `json:"media_errors"`
		ErrLogEntries    int64 `json:"num_err_log_entries"`
	} `json:"nvme_smart_health_information_log"`
	NVMeSelfTestLog *struct {
		Current *struct {
			Value int64 `json:"value"`
		} `json:"current_self_test_operation"`
		CompletionPercent *int64 `json:"current_self_test_completion_percent"`
		Table             []struct {
			Code struct {
				String string `json:"string"`
			} `json:"self_test_code"`
			Result struct {
				String string `json:"string"`
			} `json:"self_test_result"`
			PowerOnHours int64 `json:"power_on_hours"`
		} `json:"table"`
	} `json:"nvme_self_test_log"`
}

func i64(v int64) *int64 { return &v }

// smartAsleep reports smartctl -n standby skipping a sleeping disk.
func smartAsleep(out []byte) bool {
	s := string(out)
	return strings.Contains(s, "STANDBY") || strings.Contains(s, "SLEEP mode")
}

// ParseSmart reads `smartctl -j -a` for SATA/SAS and NVMe disks. smartctl's
// exit status is a bitmask that is often non-zero for healthy disks, so the
// caller parses the output even when the command "failed".
func ParseSmart(out []byte) (*SmartReport, error) {
	var j smartJSON
	if err := json.Unmarshal(out, &j); err != nil {
		return nil, fmt.Errorf("couldn't read SMART data: %w", err)
	}
	r := &SmartReport{Attributes: []SmartAttribute{}, SelfTests: []SelfTest{}}
	s := &r.Summary
	if j.SmartStatus != nil {
		passed := j.SmartStatus.Passed
		s.Passed = &passed
		s.Available = true
	}
	if j.SmartSupport != nil && !j.SmartSupport.Available {
		s.Available = false
	}
	if j.Temperature != nil && j.Temperature.Current > 0 {
		s.TemperatureC = i64(j.Temperature.Current)
	}
	if j.PowerOnTime != nil {
		s.PowerOnHours = i64(j.PowerOnTime.Hours)
	}
	if a := j.ATAAttributes; a != nil {
		s.Available = true
		for _, t := range a.Table {
			raw := t.Raw.String
			if raw == "" {
				raw = fmt.Sprint(t.Raw.Value)
			}
			failing := t.WhenFailed != "" || (t.Thresh > 0 && t.Value <= t.Thresh)
			r.Attributes = append(r.Attributes, SmartAttribute{ID: t.ID, Name: strings.ReplaceAll(t.Name, "_", " "),
				Value: t.Value, Worst: t.Worst, Thresh: t.Thresh, Raw: raw, Failing: failing})
			switch t.ID {
			case 5:
				s.Reallocated = i64(t.Raw.Value)
			case 197:
				s.Pending = i64(t.Raw.Value)
			case 194, 190:
				if s.TemperatureC == nil {
					s.TemperatureC = i64(t.Raw.Value & 0xff)
				}
			}
		}
	}
	if d := j.ATASmartData; d != nil && d.SelfTest != nil {
		// Status values 0xF0-0xFF mean a test is running.
		if v := d.SelfTest.Status.Value; v >= 240 && v <= 255 {
			s.TestRunning = true
			s.TestRemaining = d.SelfTest.Status.RemainingPercent
		}
	}
	if l := j.ATASelfTestLog; l != nil && l.Standard != nil {
		for _, t := range l.Standard.Table {
			r.SelfTests = append(r.SelfTests, SelfTest{Type: t.Type.String, Status: t.Status.String, Hours: t.LifetimeHours})
		}
	}
	if n := j.NVMeLog; n != nil {
		s.Available = true
		r.NVMe = &NVMeHealth{CriticalWarning: n.CriticalWarning, AvailableSpare: n.AvailableSpare, SpareThreshold: n.SpareThreshold,
			PercentageUsed: n.PercentageUsed, DataUnitsRead: n.DataUnitsRead, DataUnitsWritten: n.DataUnitsWritten,
			PowerCycles: n.PowerCycles, UnsafeShutdowns: n.UnsafeShutdowns, MediaErrors: n.MediaErrors, ErrorLogEntries: n.ErrLogEntries}
		s.PercentUsed = i64(n.PercentageUsed)
		s.MediaErrors = i64(n.MediaErrors)
		if s.TemperatureC == nil && n.Temperature > 0 {
			s.TemperatureC = i64(n.Temperature)
		}
		if s.PowerOnHours == nil {
			s.PowerOnHours = i64(n.PowerOnHours)
		}
	}
	if l := j.NVMeSelfTestLog; l != nil {
		if l.Current != nil && l.Current.Value != 0 {
			s.TestRunning = true
			if l.CompletionPercent != nil {
				s.TestRemaining = i64(100 - *l.CompletionPercent)
			}
		}
		for _, t := range l.Table {
			r.SelfTests = append(r.SelfTests, SelfTest{Type: t.Code.String, Status: t.Result.String, Hours: t.PowerOnHours})
		}
	}
	return r, nil
}

// smartProblem says whether a summary needs attention ("" = healthy).
func smartProblem(s *SmartSummary) (level, why string) {
	if s == nil || !s.Available {
		return "", ""
	}
	if s.Passed != nil && !*s.Passed {
		return "error", "the disk's own health check (SMART) failed"
	}
	switch {
	case s.Reallocated != nil && *s.Reallocated > 0:
		return "warning", fmt.Sprintf("%d reallocated sectors", *s.Reallocated)
	case s.Pending != nil && *s.Pending > 0:
		return "warning", fmt.Sprintf("%d sectors waiting to be reallocated", *s.Pending)
	case s.MediaErrors != nil && *s.MediaErrors > 0:
		return "warning", fmt.Sprintf("%d media errors", *s.MediaErrors)
	case s.PercentUsed != nil && *s.PercentUsed >= 90:
		return "warning", fmt.Sprintf("%d%% of its rated life used", *s.PercentUsed)
	}
	return "", ""
}
