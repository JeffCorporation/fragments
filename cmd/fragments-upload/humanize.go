package main

// French human-readable formatting for the interactive menu. The UI speaks
// French for now; keep every user-facing string in this file or main.go so a
// future i18n pass has a single place to look.

import (
	"fmt"
	"strings"
	"time"
)

// frenchAgo renders how long ago t was, in rough French ("il y a 3 h").
// Zero time (no media file found) yields a placeholder.
func frenchAgo(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "date inconnue"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "à l'instant"
	case d < time.Hour:
		return fmt.Sprintf("il y a %d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("il y a %d h", int(d.Hours()))
	case d < 48*time.Hour:
		return "hier"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("il y a %d j", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("il y a %d mois", int(d.Hours()/(24*30)))
	default:
		if y := int(d.Hours() / (24 * 365)); y > 1 {
			return fmt.Sprintf("il y a %d ans", y)
		}
		return "il y a 1 an"
	}
}

// frenchSize renders a byte count with French decimal units ("26,4 Go").
func frenchSize(b int64) string {
	const unit = 1000
	if b < 2 {
		return fmt.Sprintf("%d octet", b) // 0 and 1 take the singular
	}
	if b < unit {
		return fmt.Sprintf("%d octets", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"Ko", "Mo", "Go", "To", "Po"}
	val := fmt.Sprintf("%.1f", float64(b)/float64(div))
	return strings.ReplaceAll(val, ".", ",") + " " + units[exp]
}

// frenchCount renders "1 fichier" / "1 234 fichiers" with the French
// thousands separator (plain space: friendlier to terminals and copy-paste).
func frenchCount(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	// Zero takes the singular in French ("0 fichier").
	if n < 2 {
		return s + " fichier"
	}
	return s + " fichiers"
}
