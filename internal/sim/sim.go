// Package sim provides "virtual suits": realistic 1 Hz telemetry for crew
// members who have no phone connected, so the station can show the whole
// crew during a demonstration. It also injects failure scenarios.
//
// A real phone always wins: when a crew member's phone links, the engine
// switches that virtual suit off automatically.
package sim

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"

	"eva/internal/model"
)

// Scenario is a simulated failure (same ids as the EVA app).
type Scenario struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Description string  `json:"description"`
	Metric      string  `json:"metric"`
	Target      float64 `json:"-"`
	Sec         float64 `json:"-"`
}

// Scenarios lists every failure that can be injected.
var Scenarios = []Scenario{
	{ID: "tachycardia", Metric: "hr", Target: 168, Sec: 30, Label: "Exertional tachycardia", Description: "Heart rate climbs to about 168 bpm in 30 s."},
	{ID: "hypoxia", Metric: "spo2", Target: 87, Sec: 35, Label: "Hypoxia", Description: "Oxygen saturation drops to 87 %."},
	{ID: "overheat", Metric: "temp", Target: 38.8, Sec: 60, Label: "Cooling failure", Description: "Cooling garment stops; core temperature rises to 38.8 °C."},
	{ID: "leak", Metric: "suitP", Target: 3.5, Sec: 45, Label: "Slow pressure leak", Description: "Suit pressure falls from 4.3 to 3.5 psi and O₂ drains faster."},
	{ID: "co2", Metric: "co2", Target: 10.6, Sec: 40, Label: "Helmet CO₂ build-up", Description: "CO₂ scrubber fails; helmet CO₂ rises to 10.6 mmHg."},
	{ID: "battery", Metric: "batt", Target: 8, Sec: 40, Label: "Low suit battery", Description: "Suit battery falls to 8 %."},
	{ID: "radiation", Metric: "rad", Target: 3.2, Sec: 10, Label: "Solar particle event", Description: "Dose rate jumps to 3.2 mSv/h."},
	{ID: "sensor", Metric: "hr", Target: 0, Sec: 0, Label: "Detached HR sensor", Description: "Heart-rate strap loses contact (implausible readings)."},
	{ID: "signal", Metric: "link", Target: 0, Sec: 0, Label: "Signal loss", Description: "The suit stops sending data."},
}

func scenarioByID(id string) *Scenario {
	for i := range Scenarios {
		if Scenarios[i].ID == id {
			return &Scenarios[i]
		}
	}
	return nil
}

// Ingester receives the generated readings (the triage engine).
type Ingester interface {
	Ingest(s model.Sample, source string)
}

type scen struct {
	p        float64
	clearing bool
}

type suit struct {
	c        model.Crew
	b        model.Baseline
	on       bool
	v        map[string]float64
	o2, batt float64
	o2Full   float64 // reserves at the start of each EVA (refilled between EVAs)
	battFull float64
	effort   int
	phase    float64
	bpSys    float64
	bpDia    float64
	scen     map[string]*scen
}

// Sim runs the virtual suits.
type Sim struct {
	mu    sync.Mutex
	ing   Ingester
	suits map[string]*suit
	rnd   *rand.Rand
}

// New creates one virtual suit per EVA crew member (all disabled).
func New(ing Ingester, crew []model.Crew, bases map[string]model.Baseline) *Sim {
	s := &Sim{ing: ing, suits: map[string]*suit{}, rnd: rand.New(rand.NewSource(time.Now().UnixNano()))}
	for i, c := range crew {
		if !c.IsEVA() {
			continue
		}
		b := bases[c.ID]
		if b.HRRest == 0 {
			b = model.Baseline{HRRest: 66, SpO2: 98, Temp: 36.6, RR: 14, HRV: 50, BPSys: 120, BPDia: 78}
		}
		su := &suit{c: c, b: b, o2: 372 + 8*float64(i), batt: 79 + 3*float64(i), phase: float64(i) * 97, scen: map[string]*scen{},
			bpSys: b.BPSys + 4, bpDia: b.BPDia + 2}
		su.o2Full, su.battFull = su.o2, su.batt
		su.v = map[string]float64{"hr": b.HRRest + 30, "spo2": b.SpO2 - 0.6, "temp": b.Temp + 0.35, "rr": b.RR + 5, "hrv": b.HRV * 0.7,
			"rad": 0.055, "suitP": 4.3, "co2": 2.3}
		s.suits[c.ID] = su
	}
	return s
}

// Enabled reports whether the virtual suit of a crew member is running.
func (s *Sim) Enabled(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	su := s.suits[id]
	return su != nil && su.on
}

// SetEnabled switches a virtual suit on or off.
func (s *Sim) SetEnabled(id string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if su := s.suits[id]; su != nil {
		su.on = on
	}
}

// Has reports whether a crew member can have a virtual suit.
func (s *Sim) Has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.suits[id] != nil
}

// Inject starts a failure scenario on a virtual suit.
func (s *Sim) Inject(id, scenario string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	su := s.suits[id]
	if su == nil || scenarioByID(scenario) == nil {
		return false
	}
	if x := su.scen[scenario]; x != nil {
		x.clearing = false
	} else {
		su.scen[scenario] = &scen{}
	}
	return true
}

// Clear removes the failures of one suit ("" = all); values recover in ~20 s.
func (s *Sim) Clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sid, su := range s.suits {
		if id != "" && sid != id {
			continue
		}
		for k, x := range su.scen {
			if k == "signal" || k == "sensor" {
				delete(su.scen, k)
				continue
			}
			x.clearing = true
		}
	}
}

// Active returns the active scenario ids of every suit.
func (s *Sim) Active() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]string{}
	for id, su := range s.suits {
		var a []string
		for k, x := range su.scen {
			if !x.clearing {
				a = append(a, k)
			}
		}
		sort.Strings(a)
		out[id] = a
	}
	return out
}

// States returns which suits are enabled.
func (s *Sim) States() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for id, su := range s.suits {
		out[id] = su.on
	}
	return out
}

// Run generates one reading per enabled suit every second until stop closes.
func (s *Sim) Run(stop <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.mu.Lock()
			var out []model.Sample
			now := time.Now()
			for _, su := range s.suits {
				if !su.on {
					continue
				}
				if smp, ok := s.step(su, now); ok {
					out = append(out, smp)
				}
			}
			s.mu.Unlock()
			for _, smp := range out { // outside the lock: the engine has its own
				s.ing.Ingest(smp, "virtual")
			}
		}
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
func ease(t float64) float64       { t = clamp(t, 0, 1); return t * t * (3 - 2*t) }
func round(v float64, d int) float64 {
	f := math.Pow(10, float64(d))
	return math.Round(v*f) / f
}

// step advances one suit by one second (Ornstein–Uhlenbeck style signals).
func (s *Sim) step(su *suit, now time.Time) (model.Sample, bool) {
	// work cycle of 6 minutes: rest → moderate → high → moderate
	cyc := math.Mod(float64(now.Unix())+su.phase, 360)
	switch {
	case cyc < 60:
		su.effort = model.EffortRest
	case cyc < 240:
		su.effort = model.EffortModerate
	case cyc < 330:
		su.effort = model.EffortHigh
	default:
		su.effort = model.EffortModerate
	}
	targets := map[string][2]float64{} // metric → (target, ease)
	o2mult, hrBoost := 1.0, 0.0
	for id, x := range su.scen {
		def := scenarioByID(id)
		if def == nil {
			continue
		}
		if x.clearing {
			x.p -= 1.0 / 20
			if x.p <= 0 {
				delete(su.scen, id)
				continue
			}
		} else if def.Sec > 0 {
			x.p = math.Min(1, x.p+1/def.Sec)
		} else {
			x.p = 1
		}
		e := ease(x.p)
		if def.Metric != "link" && id != "sensor" {
			targets[def.Metric] = [2]float64{def.Target, e}
		}
		if id == "leak" {
			o2mult = 1 + 11*e
		}
		if id == "overheat" {
			hrBoost = 10 * e
		}
	}
	if x := su.scen["signal"]; x != nil && !x.clearing {
		return model.Sample{}, false
	}
	b := su.b
	mean := map[string]float64{
		"hr":    b.HRRest + []float64{8, 30, 55}[su.effort] + hrBoost,
		"spo2":  b.SpO2 - 0.3 - 0.4*float64(su.effort),
		"temp":  b.Temp + 0.25 + 0.15*float64(su.effort),
		"rr":    b.RR + []float64{0, 5, 10}[su.effort],
		"hrv":   b.HRV * []float64{1, 0.72, 0.5}[su.effort],
		"rad":   0.055,
		"suitP": 4.3,
		"co2":   2.2 + 0.4*float64(su.effort),
	}
	dyn := map[string][2]float64{ // theta, sigma
		"hr": {0.12, 1.3}, "spo2": {0.12, 0.12}, "temp": {0.05, 0.01}, "rr": {0.15, 0.5}, "hrv": {0.1, 1.5},
		"rad": {0.3, 0.004}, "suitP": {0.2, 0.006}, "co2": {0.06, 0.04},
	}
	for k, m := range mean {
		th, sg := dyn[k][0], dyn[k][1]
		if tg, ok := targets[k]; ok {
			m = m + (tg[0]-m)*tg[1]
			sg *= 1 - 0.5*tg[1]
			th = math.Max(th, 0.15)
		}
		su.v[k] += th*(m-su.v[k]) + sg*s.rnd.NormFloat64()
	}
	su.v["spo2"] = clamp(su.v["spo2"], 70, 100)
	su.v["rad"] = math.Max(0.01, su.v["rad"])
	// Consumables: an 8 h EVA uses ~60 % battery. When either reserve would
	// reach its caution band (O2 < 90 min, battery < 35 %) without a fault
	// scenario forcing it, the suit is swapped/recharged as between two EVAs,
	// so a station left running for hours never sticks on a false CRITICAL.
	su.o2 = math.Max(0, su.o2-o2mult/60)
	su.batt = math.Max(0, su.batt-0.002)
	if (su.o2 < 95 && su.scen["leak"] == nil) || (su.batt < 38 && su.scen["battery"] == nil) {
		su.o2, su.batt = su.o2Full, su.battFull
	}
	batt := su.batt
	if tg, ok := targets["batt"]; ok {
		batt = su.batt + (tg[0]-su.batt)*tg[1]
	}
	hr := round(su.v["hr"], 0)
	if x := su.scen["sensor"]; x != nil && !x.clearing {
		hr = []float64{0, 0, 238, 0, 12}[now.Unix()%5] // detached strap: implausible values
	}
	smp := model.Sample{
		CrewID: su.c.ID, T: now.UnixMilli(),
		HR: model.F(hr), SpO2: model.F(round(su.v["spo2"], 0)), Temp: model.F(round(su.v["temp"], 1)),
		RR: model.F(round(su.v["rr"], 0)), HRV: model.F(round(math.Max(5, su.v["hrv"]), 0)), Rad: model.F(round(su.v["rad"], 3)),
		Effort: model.I(su.effort), SuitP: model.F(round(su.v["suitP"], 2)), O2: model.F(round(su.o2, 0)),
		CO2: model.F(round(math.Max(0, su.v["co2"]), 1)), Batt: model.F(round(batt, 0)),
		BPSys: model.F(su.bpSys), BPDia: model.F(su.bpDia), Source: "virtual",
	}
	return smp, true
}
