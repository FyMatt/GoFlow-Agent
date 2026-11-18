package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

func cronNext(trigger Trigger, after time.Time) (time.Time, error) {
	parsed, err := cron.ParseStandard(strings.TrimSpace(trigger.Expression))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression: %w", err)
	}
	location := time.UTC
	if name := strings.TrimSpace(trigger.Timezone); name != "" {
		location, err = time.LoadLocation(name)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid cron timezone %q: %w", name, err)
		}
	}
	return parsed.Next(after.In(location)).UTC(), nil
}
