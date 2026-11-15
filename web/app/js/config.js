/* EVA · Editable configuration: identity, mission, crew, risk thresholds, mental health text, voice, and simulation. Values are examples. */
window.App = window.App || {};

App.config = {
  /* Identity */
  app: {
    name: 'EVA',
    tagline: 'EVA biomedical support',
    version: '1.0',
    logo: '../shared/logo.png',      // Shared mission logo
    splashMs: 2800,               // Splash duration
    defaultTheme: 'light',        // 'dark' / 'light' / 'system'
  },

  /* Mission */
  mission: {
    name: 'SELENE-3',
    destination: 'Moon',                       // 'Moon' or 'Mars'
    site: 'Shackleton crater · south pole',
    evaNumber: 2,
    earthDelay: { Moon: '1.3 s', Mars: '4–24 min' },   // One-way signal delay to Earth
  },

  /* Crew roles: EV1 and EV2 walk outside with suit monitoring; IV operates the app inside. Reassign roles in Settings > Assign roles. */
  crew: [
    {
      id: 'AST-001', name: 'Ana Ruiz', initials: 'AR', role: 'EV1',
      age: 38, sex: 'Female', specialty: 'Planetary geologist', bloodType: 'A+',
      medical: {
        allergies: 'Penicillin',
        conditions: 'No relevant history',
        medication: 'None',
        surgeries: 'Appendectomy (2009)',
        evaHours: 21,
      },
      baseline: { hr: 62, bpSys: 116, bpDia: 74, spo2: 98, temp: 36.6, rr: 14, hrv: 58, weight: 61, height: 167, vo2max: 46, measuredAt: '2026-10-28' },
    },
    {
      id: 'AST-002', name: 'Luis Ortega', initials: 'LO', role: 'EV2',
      age: 44, sex: 'Male', specialty: 'Systems engineer', bloodType: 'O+',
      medical: {
        allergies: 'None known',
        conditions: 'Mild hypertension, controlled',
        medication: 'Losartan 25 mg daily',
        surgeries: 'None',
        evaHours: 34,
      },
      baseline: { hr: 68, bpSys: 124, bpDia: 80, spo2: 97, temp: 36.7, rr: 15, hrv: 48, weight: 78, height: 178, vo2max: 42, measuredAt: '2026-10-28' },
    },
    {
      id: 'AST-003', name: 'Marta Vidal', initials: 'MV', role: 'IV',
      age: 41, sex: 'Female', specialty: 'Flight physician', bloodType: 'B+',
      medical: {
        allergies: 'None known',
        conditions: 'Corrected myopia',
        medication: 'None',
        surgeries: 'Refractive surgery (2016)',
        evaHours: 12,
      },
      baseline: { hr: 64, bpSys: 112, bpDia: 72, spo2: 98, temp: 36.5, rr: 13, hrv: 62, weight: 64, height: 170, vo2max: 44, measuredAt: '2026-10-28' },
    },
  ],

  /* Risk levels (0 to 3) */
  levels: [
    { id: 0, key: 'nominal',  label: 'Nominal',     short: 'OK',    icon: 'check',    voice: 'nominal' },
    { id: 1, key: 'caution',  label: 'Caution',  short: 'CAUT.', icon: 'info',     voice: 'caution' },
    { id: 2, key: 'warning',  label: 'High risk', short: 'HIGH',  icon: 'triangle', voice: 'high risk' },
    { id: 3, key: 'critical', label: 'Critical',     short: 'CRIT.', icon: 'octagon',  voice: 'critical' },
  ],

  /* Suit metrics and thresholds: within nominal = 0; outside nominal but within caution = 1; outside caution but within warning = 2; outside warning = 3. Ranges are [minimum, maximum]; null means unbounded. */
  metrics: {
    hr:    { label: 'Heart rate',   short: 'HR',      unit: 'bpm',  group: 'bio',  icon: 'heart',       decimals: 0,
             nominal: [50, 115],  caution: [45, 135],  warning: [40, 155], effortShift: [0, 25, 50], low: 'low', high: 'elevated' },
    bp:    { label: 'Blood pressure',      short: 'BP',      unit: 'mmHg', group: 'bio',  icon: 'gauge',       decimals: 0,
             nominal: [90, 140],  caution: [85, 160],  warning: [80, 180],  low: 'low', high: 'high',
             onDemand: true },   // Measured on demand; classify using systolic pressure
    spo2:  { label: 'Oxygen saturation', short: 'SpO₂',    unit: '%',    group: 'bio',  icon: 'droplet',     decimals: 0,
             nominal: [94, null], caution: [92, null], warning: [90, null], low: 'low', high: 'high' },
    temp:  { label: 'Body temperature',  short: 'Temp.',   unit: '°C',   group: 'bio',  icon: 'thermometer', decimals: 1,
             nominal: [36.0, 37.6], caution: [35.5, 38.0], warning: [35.0, 39.0], low: 'low', high: 'high' },
    rr:    { label: 'Respiratory rate', short: 'Resp.', unit: 'rpm', group: 'bio', icon: 'wind', decimals: 0,
             nominal: [10, 24], caution: [8, 30], warning: [6, 36], low: 'low', high: 'high' },
    hrv:   { label: 'Heart-rate variability', short: 'HRV', unit: 'ms', group: 'bio', icon: 'activity', decimals: 0,
             nominal: [30, null], caution: [22, null], warning: [15, null], low: 'low', high: 'high' },
    rad:   { label: 'Radiation dose rate', short: 'Dose', unit: 'mSv/h', group: 'suit', icon: 'planet', decimals: 2,
             nominal: [null, 0.15], caution: [null, 0.5], warning: [null, 2], low: 'low', high: 'high' },
    suitP: { label: 'Suit pressure',     short: 'Pressure', unit: 'psi',  group: 'suit', icon: 'suit',        decimals: 2,
             nominal: [4.1, 4.5], caution: [3.9, 4.7], warning: [3.6, 4.9], low: 'low', high: 'high' },
    o2:    { label: 'Oxygen reserve',    short: 'O₂',      unit: 'min',  group: 'suit', icon: 'tank',        decimals: 0,
             nominal: [90, null], caution: [45, null], warning: [20, null], low: 'low', high: 'high' },
    co2:   { label: 'Helmet CO₂',       short: 'CO₂',     unit: 'mmHg', group: 'suit', icon: 'wind',        decimals: 1,
             nominal: [null, 5],  caution: [null, 7.6], warning: [null, 10], low: 'low', high: 'high' },
    batt:  { label: 'Suit battery',     short: 'Battery', unit: '%',    group: 'suit', icon: 'battery',     decimals: 0,
             nominal: [35, null], caution: [20, null], warning: [10, null], low: 'low', high: 'high' },
  },

  /* Metric display order */
  groups: {
    bio:  { label: 'Physical health',          metrics: ['hr', 'bp', 'spo2', 'temp', 'rr', 'hrv'] },
    suit: { label: 'Suit and life support', metrics: ['suitP', 'o2', 'co2', 'batt', 'rad'] },
  },

  /* Mental health check-in */
  mental: {
    hydration: { label: 'Hydration', options: ['Good', 'Fair', 'Low'], values: [3, 5, 7] },
    mood:   { label: 'Mood',   options: ['Low', 'Steady', 'Good'], icons: ['moodLow', 'moodMid', 'moodHigh'] },
    energy: { label: 'Energy', options: ['Low', 'Medium', 'High'],    icons: ['energyLow', 'energyMid', 'energyHigh'] },
    sleep:  { label: 'Sleep', recommendedMin: 420, stepMin: 15, defaultMin: 435, maxMin: 720 },
  },

  /* Voice */
  voice: {
    lang: 'en-US',                // Prefer US English; fall back to any English voice
    rate: 1.05,                   // Speech rate (0.5 to 2)
    pitch: 1,
    minLevelToSpeak: 1,           // 1 = speak at caution and above
    repeatCriticalEverySec: 30,   // Repeat critical alerts while active
  },

  /* Simulation (working concept) */
  sim: {
    tickMs: 1000,                 // Interval between readings
    historyLength: 60,            // Sparkline points (60 = last minute)
    autoConnect: ['hr', 'spo2', 'temp', 'rr', 'hrv', 'suitP', 'o2', 'co2', 'batt', 'rad'],  // Sensors connected automatically at startup
    blackboxEverySec: 5,          // Telemetry snapshot interval
    blackboxMaxEvents: 4000,      // Maximum events stored on the device
  },

  storage: { prefix: 'eva.' },
};
