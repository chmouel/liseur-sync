package webui

import (
	"fmt"
	"strconv"

	"github.com/chmouel/liseur-sync/internal/store"
)

// userCtx carries the signed-in user into templates.
type userCtx struct {
	User *store.User
}

// SessionRow is one recent session for the dashboard.
type SessionRow struct {
	When      string
	WorkID    string
	WorkTitle string
	DeviceID  string
	Minutes   int
	StartProg float64
	EndProg   float64
}

func pct(f float64) string { return strconv.Itoa(int(f*100)) + "%" }
func f0(f float64) string  { return strconv.FormatFloat(f, 'f', 0, 64) }
func i2s(i int) string     { return strconv.Itoa(i) }

func orPlaceholder(s string) string {
	if s == "" {
		return "(untitled)"
	}
	return s
}

func barWidth(f float64) string {
	return fmt.Sprintf("width:%d%%", int(f*100))
}

// cellClass buckets minutes for the heatmap.
func cellClass(minutes float64) string {
	switch {
	case minutes <= 0:
		return "cell c0"
	case minutes < 15:
		return "cell c1"
	case minutes < 45:
		return "cell c2"
	case minutes < 90:
		return "cell c3"
	default:
		return "cell c4"
	}
}
