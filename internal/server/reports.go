package server

import (
	_ "embed"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"eva/internal/engine"
	"eva/internal/model"
)

//go:embed reports.tmpl.html
var reportsTmpl string

var tmpl = template.Must(template.New("reports").Funcs(template.FuncMap{
	"clock": func(ms int64) string { return time.UnixMilli(ms).Format("15:04:05") },
	"when":  func(ms int64) string { return time.UnixMilli(ms).Format("02 Jan 2006 · 15:04") },
	"whenp": func(p *int64) string {
		if p == nil {
			return "—"
		}
		return time.UnixMilli(*p).Format("02 Jan 2006 · 15:04:05")
	},
	"clockp": func(p *int64) string {
		if p == nil {
			return "—"
		}
		return time.UnixMilli(*p).Format("15:04:05")
	},
	"dur": func(s int64) string {
		if s < 60 {
			return fmt.Sprintf("%d s", s)
		}
		if s < 3600 {
			return fmt.Sprintf("%d min %02d s", s/60, s%60)
		}
		return fmt.Sprintf("%d h %02d min", s/3600, (s%3600)/60)
	},
	"f0": fnum(0), "f1": fnum(1), "f2": fnum(2), "f3": fnum(3),
	"lower": strings.ToLower,
	"human": func(s string) string { return strings.ReplaceAll(strings.ToLower(s), "_", " ") },
	"alertLevel": func(a model.Alert) int { return a.LevelNum() },
	"ipvLevel": func(ipv int) int {
		switch {
		case ipv >= 90:
			return 3
		case ipv >= 70:
			return 2
		case ipv >= 40:
			return 1
		}
		return 0
	},
	"statusLevel": func(s string) int {
		return map[string]int{"RED": 3, "YELLOW": 1, "GREEN": 0, "SUDDEN": 2, "PROGRESSIVE": 1, "SENSOR_FAULT": 0}[s]
	},
	"chart": chartSVG,
	"now":   func() string { return time.Now().Format("02 Jan 2006 · 15:04") },
	"add":   func(a, b int) int { return a + b },
	"dict3": func(eyebrow, title, sub string) map[string]string {
		return map[string]string{"Eyebrow": eyebrow, "Title": title, "Sub": sub}
	},
	"sexWord": func(s string) string {
		if s == "M" {
			return "Male"
		}
		return "Female"
	},
}).Parse(reportsTmpl))

func fnum(d int) func(v any) string {
	return func(v any) string {
		var x float64
		switch t := v.(type) {
		case float64:
			x = t
		case *float64:
			if t == nil {
				return "—"
			}
			x = *t
		case int:
			x = float64(t)
		case int64:
			x = float64(t)
		case *int:
			if t == nil {
				return "—"
			}
			x = float64(*t)
		default:
			return "—"
		}
		if math.IsNaN(x) {
			return "—"
		}
		return strconv.FormatFloat(x, 'f', d, 64)
	}
}

// chartSVG draws one frozen series with the event marker and a shaded
// reference band [lo, hi] (the nominal range).
func chartSVG(title, unit string, t []int64, ys []*float64, eventT int64, lo, hi float64) template.HTML {
	const W, H, pl, pr, pt, pb = 760.0, 150.0, 44.0, 12.0, 22.0, 24.0
	var b strings.Builder
	fmt.Fprintf(&b, `<figure class="chart"><svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s over the incident window">`, W, H, template.HTMLEscapeString(title))
	if len(t) < 2 {
		b.WriteString(`<text x="20" y="80" class="chart__empty">No readings in this window</text></svg></figure>`)
		return template.HTML(b.String())
	}
	t0, t1 := float64(t[0]), float64(t[len(t)-1])
	if eventT > 0 {
		t0 = math.Min(t0, float64(eventT))
		t1 = math.Max(t1, float64(eventT))
	}
	mn, mx := math.Inf(1), math.Inf(-1)
	for _, y := range ys {
		if y != nil {
			mn, mx = math.Min(mn, *y), math.Max(mx, *y)
		}
	}
	if math.IsInf(mn, 1) {
		mn, mx = lo, hi
	}
	mn, mx = math.Min(mn, lo), math.Max(mx, hi)
	span := mx - mn
	if span < 1e-6 {
		span = 1
	}
	mn -= span * 0.08
	mx += span * 0.08
	x := func(v float64) float64 {
		if t1 == t0 {
			return pl
		}
		return pl + (v-t0)/(t1-t0)*(W-pl-pr)
	}
	y := func(v float64) float64 { return pt + (mx-v)/(mx-mn)*(H-pt-pb) }
	fmt.Fprintf(&b, `<rect class="chart__band" x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, pl, y(hi), W-pl-pr, math.Max(0, y(lo)-y(hi)))
	fmt.Fprintf(&b, `<text class="chart__title" x="%.0f" y="14">%s (%s)</text>`, pl, template.HTMLEscapeString(title), template.HTMLEscapeString(unit))
	fmt.Fprintf(&b, `<text class="chart__axis" x="%.0f" y="%.1f" text-anchor="end">%.1f</text>`, pl-6, y(mx)+4, mx)
	fmt.Fprintf(&b, `<text class="chart__axis" x="%.0f" y="%.1f" text-anchor="end">%.1f</text>`, pl-6, y(mn), mn)
	fmt.Fprintf(&b, `<text class="chart__axis" x="%.0f" y="%.0f">%s</text>`, pl, H-6, time.UnixMilli(int64(t0)).Format("15:04"))
	fmt.Fprintf(&b, `<text class="chart__axis" x="%.0f" y="%.0f" text-anchor="end">%s</text>`, W-pr, H-6, time.UnixMilli(int64(t1)).Format("15:04"))
	// line, broken where data is missing or there is a gap > 10 s
	var path strings.Builder
	pen := false
	for i, v := range ys {
		if v == nil || (i > 0 && t[i]-t[i-1] > 10000) {
			pen = false
			if v == nil {
				continue
			}
		}
		if !pen {
			fmt.Fprintf(&path, "M%.1f %.1f", x(float64(t[i])), y(*v))
			pen = true
		} else {
			fmt.Fprintf(&path, "L%.1f %.1f", x(float64(t[i])), y(*v))
		}
	}
	fmt.Fprintf(&b, `<path class="chart__line" d="%s"/>`, path.String())
	if eventT > 0 {
		ex := x(float64(eventT))
		fmt.Fprintf(&b, `<line class="chart__event" x1="%.1f" x2="%.1f" y1="%.0f" y2="%.0f"/><text class="chart__eventlabel" x="%.1f" y="%.0f">T0</text>`,
			ex, ex, pt-4, H-pb, ex+4, pt+8)
	}
	b.WriteString(`</svg></figure>`)
	return template.HTML(b.String())
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func (s *Server) reportMission(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Eng.MissionReport(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	s.render(w, "mission", rep)
}

func (s *Server) reportIncident(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rep, err := s.Eng.Incident(id)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	s.render(w, "incident", struct {
		engine.IncidentReport
	}{rep})
}

func (s *Server) reportMonthly(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Eng.MonthlyReport(r.PathValue("crew"), r.URL.Query().Get("month"))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	s.render(w, "monthly", rep)
}
