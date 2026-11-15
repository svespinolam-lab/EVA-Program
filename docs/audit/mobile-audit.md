# EVA phone app — read-only audit (annex to AUDITORIA-FASE-1.md, produced with Codex)

Date: 2026-11-15. Scope: `web/app`, `web/shared`, `/ws/device`, relevant Go engine/store/report code. No repository files were written. Evidence: source inspection and isolated Node VM probes with mock browser/storage/BLE objects; no physical sensors or live station tested. Clinical thresholds were compared for implementation consistency, not medically validated.

**Snapshot caveat:** other processes changed station code during inspection, including `cmd/eva-station/main.go`, `internal/server/server.go`, `internal/engine/engine.go` and `internal/store/store.go`, and added authentication/test files. Station citations use the refreshed version with operator-route protection and a public-only welcome roster. These external changes were not made by this audit.

**Overall:** a working demo shell, profile editor, three-field mental check-in, local event log, and telemetry store-and-forward exist. The full brief is not implemented. Critical defects include lost manual measurements on reload, mixed real/demo provenance, simulated histories accompanying hardware values, unreachable BLE/lab/sync controls, and valid monthly dates being rejected.

Citation shorthand: `config.js`, `core.js`, `app.js` are under `web/app/js/`; `log.js`, `diagnosis.js`, `profile.js`, `settings.js` under `web/app/js/views/`; `telemetry.js`, `ble.js`, `link.js`, `risk.js`, `blackbox.js` under `web/app/js/engine/`. Go paths are repository-relative.

## A. Astronaut profile

| Requirement | Status | Evidence / gap |
|---|---|---|
| Identity/personal data | IMPLEMENTED | Name/specialty display, edit, validation and persisted overrides: `profile.js:114–135,200–233`; merge: `core.js:86–94`. Initial identities are examples. |
| Age | IMPLEMENTED | Editable age, 18–80 validation, saved override (`profile.js:121,208,215,229`). |
| Biological sex | PARTIAL | Persisted sex selector (`profile.js:11,122–124,230`); not explicitly distinguished as biological. Phone uses Female/Male/Other/Prefer not to say; station clinical model expects F/M (`internal/model/model.go:53`). No mapping/sync. |
| Height | PARTIAL | Read-only seeded cm baseline; no capture/update workflow (`profile.js:26,172–186`; `config.js:36,48,60`). |
| Weight | PARTIAL | Seeded baseline/BMI (`profile.js:25,71`). Separate current mass input (`diagnosis.js:444`) does not update profile and is lost on reload. |
| Crew ID | PARTIAL | Stable AST-* IDs drive state/link (`config.js:27,39,51`; `link.js:118`), but profile omits the ID (`profile.js:142–152`); no enrollment. |
| Mission | PARTIAL | Static configuration displayed (`profile.js:149`); received `stationMission` is not consumed by this view (`link.js:135–140`). |
| Destination | PARTIAL | Static destination display (`profile.js:150`; `config.js:16–22`), not synchronized with station environment. |
| Duration | MISSING | No planned duration in phone config/profile. T+ is elapsed EVA time (`app.js:126–128`), not mission duration. Station has `plannedH` (`internal/model/model.go:96`). |
| Medical history | PARTIAL | Seeded conditions/surgeries/EVA hours shown, no capture/editing (`profile.js:158–169`). |
| Allergies | PARTIAL | Seeded allergies shown/highlighted, not editable (`profile.js:160,164`). |
| Medication | PARTIAL | Seeded medication string only (`profile.js:166`). |
| Supplements | MISSING | No field in medical configuration/rendering (`config.js:29–35`; `profile.js:158–169`). |
| Blood type | PARTIAL | Seeded value displayed; no capture/editing (`profile.js:148`; `config.js:28,40,52`). |
| Medical background | PARTIAL | Static conditions/surgeries/prior EVA hours; no broader structured record (`profile.js:165–168`). |
| Pre-mission measurements | PARTIAL | Dated tiles for HR/BP/SpO2/temp/weight/height/VO2 max/BMI, all configured examples; no capture (`profile.js:20–29,172–186`). |
| Personal biometric baseline | PARTIAL | Configured per-person values and current comparisons (`profile.js:81–94`; `diagnosis.js:317–371`); no acquisition/history or station adaptive-baseline consumption. |
| Emergency contacts | MISSING | No field/model/UI in audited phone profile/configuration. |

## B. Physical health — all 12 parameters

All 12 cards render (`diagnosis.js:434–446,492–498`), including for IV crew (`diagnosis.js:675–677`). Each supplies a readout, generic source, method and expandable guidance, but no per-reading timestamp, seven-day chart or computed traffic light (`diagnosis.js:481–490`). Colored six-metric tables are separate.

| Parameter | Status | Existing behavior; missing/broken behavior |
|---|---|---|
| Heart rate | PARTIAL | Value/unit, baseline, colored live row, short chart, method/advice (`log.js:62–77,213–241`; `diagnosis.js:435,455`). BLE decoder exists but pairing UI unreachable. No reading time/device identity/7-day trend; hardware chart remains simulated. |
| HRV | PARTIAL | Demo value, ms, baseline and guidance (`config.js:86–87,101`; `diagnosis.js:436,456`). No real HRV derivation/adapter, manual fallback, reading timestamp or 7-day trend. RMSSD/overnight are labels, not measurements. |
| Blood pressure | PARTIAL | Systolic/diastolic inputs, mmHg, baseline, manual label/time-of-day (`diagnosis.js:437,457,489,529`; `log.js:245–258`). Values volatile; table/risk/profile/sync disagree on source. No cuff integration, full date or 7-day trend. |
| Orthostatic pressure | PARTIAL | Single manual drop, mmHg, lying/standing at 1 and 3 minutes instructions (`diagnosis.js:438,458`). No posture/time series or separate systolic/diastolic drops, automatic assessment, baseline/history/persistence. |
| SpO2 | PARTIAL | Value/%, baseline, colored row/short chart, method/advice; PLX continuous decoder (`diagnosis.js:439,459`; `log.js:213–241`; `ble.js:40`). Pairing unreachable; no reading time/device identity/7-day history. |
| Body temperature | PARTIAL | Value/°C, baseline, colored row/short chart, method/advice (`diagnosis.js:440,460`; `log.js:213–241`). Decoder ignores flags; no reading time/device identity/7-day history. |
| Respiratory rate | PARTIAL | Manual breaths/min card (`diagnosis.js:441,461`) plus different simulated rr/baseline in bio table (`config.js:84–85,101`; `diagnosis.js:317–371`). Manual entry does not drive table/risk/sync; no real adapter or 7-day history. |
| Fasting glucose | PARTIAL | Manual mg/dL/ranges (`diagnosis.js:442,462`). No fasting-status field despite “record fasting status”; no adapter, timestamp, baseline, computed light, history or persistence. |
| Sleep + sleep quality | PARTIAL | Duration check-in persists with timestamp (`log.js:136–143,183–189`); separate physical hours input (`diagnosis.js:443,463`) is unlinked/volatile. Quality MISSING; no 7-day display or personal sleep baseline. |
| Body weight | PARTIAL | Manual kg, seeded profile baseline, text change thresholds (`diagnosis.js:444,464`; `profile.js:25`). No dated series/change calculation, computed light or persistent current weight. |
| Hydration | PARTIAL | Manual urine-color scale 1–8/guidance (`diagnosis.js:445,465`). No color-card display, integer/range validation, timestamp/history/baseline/light. Readiness ignores it and defaults to 3. |
| Accumulated radiation | PARTIAL | Manual monthly mSv (`diagnosis.js:446,467`), no mission/career accumulation or durable series. Simulated rad is dose rate in mSv/h (`config.js:88–89`), not cumulative. Station accumulation not consumed. |

### Attribute status for EACH parameter

I = IMPLEMENTED; P = PARTIAL; M = MISSING; B = BROKEN. “Current” means a value can display, not that it is verified. “Light” means computed health status, not Green/Red prose.

| Parameter | Current | Unit | Date/time | Source | Sensor status | G/Y/R light | 7-day trend | Baseline | How to measure | What to do | Auto/manual |
|---|---|---|---|---|---|---|---|---|---|---|---|
| HR | P | I | M | P | P | P | M | P | P | P | P |
| HRV | P | I | M | P | P | P | M | P | P | P | P |
| BP | P | I | P | P | M | P | M | P | P | P | B |
| Orthostatic | P | I | M | P | M | M | M | M | P | P | P |
| SpO2 | P | I | M | P | P | P | M | P | P | P | P |
| Temperature | P | I | M | P | P | P | M | P | P | P | P |
| Respiratory rate | P | I | M | B | P | P | M | P | P | P | B |
| Fasting glucose | P | I | M | P | M | M | M | M | P | P | P |
| Sleep/quality | P | I | P | P | M | P | M | M | P | P | P |
| Weight | P | I | M | P | M | M | M | P | P | P | P |
| Hydration | P | I | M | P | M | M | M | M | P | P | P |
| Accumulated radiation | P | I | M | P | M | M | M | M | P | P | P |

Shared evidence for those cells:

- **Date/time:** cards omit it (`diagnosis.js:490`); only BP saves bpAt and shows HH:MM (`diagnosis.js:529`; `log.js:252`), and check-ins show time (`diagnosis.js:264–268`). Other manual fields save only the number (`diagnosis.js:528`). Global update time is not acquisition time.
- **Source/status:** “Manual entry / external device” and connected/demo labels (`diagnosis.js:483–486`) do not identify device, acquisition time, sensor battery or invalid/stale state. Manual sensor status is omitted rather than explicitly not applicable.
- **Lights:** six bio metrics have four-level colors (`config.js:65–87,101`; `diagnosis.js:350–371`; `web/shared/css/tokens.css:52–55`). Daily chips indicate connection, not health (`diagnosis.js:490,515–516`). Sleep has a short-sleep warning, not a complete traffic light.
- **7-day trends:** absent for all 12. History is 60 un-timestamped in-memory points (`config.js:125`; `telemetry.js:124–126,382–385`); risk uses last 10 (`risk.js:65–76`). DailyLog retains up to 30 dates but is not plotted.
- **Baselines:** six metrics compare configured examples in a separate table (`diagnosis.js:317–371`); weight baseline is in Profile. Others lack personal baselines.
- **Instructions/actions:** all cards have method/rationale/guidance (`diagnosis.js:435–470,490`), often only a device name or generic referral; written rules are not implemented for manual values.
- **Auto/manual:** tags exist, but manual BP/RR do not replace engine values and mixed packets lack per-metric origin. “External device” does not distinguish typed versus automatically acquired.

### Physical-data defects

1. **BROKEN persistence:** `App.store.set({physicalManual:all})` (`diagnosis.js:531`) neither persists nor restores because core omits the key (`core.js:54–69`). All manual health entries disappear on reload, despite “Manual entries are stored on this phone only” (`diagnosis.js:498`).
2. **BROKEN histories:** display uses `sim.hardware[k]` (`telemetry.js:357`), but history records `sim.val[k]` (`telemetry.js:194–196`). Hardware HR accompanies fabricated chart/trend wording (`risk.js:66–76,146`).
3. **BROKEN freshness:** hardware stores numbers without per-metric timestamps/timeouts (`telemetry.js:366–373`). Simulator renews packet time (`telemetry.js:192,355`); link stamps Date.now again (`link.js:311`). A silent still-connected device appears current indefinitely.
4. **BROKEN manual interpretation:** no finite/physiological/range validation (`diagnosis.js:521–531`). Risk reads telemetry only (`risk.js:226–276`). BP table shows manual values but may use telemetry findings (`diagnosis.js:50–69,327–332`); Log says “Not assessed as live suit telemetry” (`log.js:251`). RR/sleep each have independent sources.
5. **Contradictory rules:** HR text says green 50–90/red >110 at rest (`diagnosis.js:455`), config nominal extends to 115 (`config.js:75`). BP text red ≥140/90 (`diagnosis.js:457`) versus systolic-only nominal through 140 (`config.js:76–78`). Log omits effort (`log.js:218,334`), risk passes it (`risk.js:266`).

## C. Periodic analyses

| Item | Status | Evidence / gap |
|---|---|---|
| Hemoglobin | BROKEN | Definition/guidance (`diagnosis.js:450,468`) never rendered: physical section maps only PHYSICAL, not MONTHLY_LABS (`diagnosis.js:498`). No reachable input/report. |
| Leukocytes | BROKEN | wbc definition/guidance (`diagnosis.js:451,469`) also never rendered. |
| Platelets | BROKEN | Definition/guidance (`diagnosis.js:452,470`) also never rendered. |
| Glucose | PARTIAL | Volatile daily entry and monthly send branch (`diagnosis.js:442,643`); no month selector/history/report; sync unreachable and validator broken. |
| Weight change | MISSING | Weight input/prose only, no percentage/history computation. Station has weightChangePct (`internal/model/model.go:211`); phone never sends it (`link.js:48`). |
| Monthly radiation | PARTIAL | Volatile cumulative input (`diagnosis.js:446`), no assigned month/history or working sync. |
| Exercise compliance | MISSING | No exercise logging/targets/calculation/UI. Station supports exercisePct (`internal/model/model.go:212`; `internal/engine/reports.go:313–314`); phone omits it. |

**Shared blockers:** monthly-sync handler (`diagnosis.js:642–643`) has no matching rendered button. Direct API invocation also rejects valid months: `link.js:46` uses `/^\\d{4}-\\d{2}$/` in a regex literal, matching literal backslashes rather than digit classes. Probe `{month:'2026-11',glucose:99}` throws **“Monthly panel requires YYYY-MM”**. Station correctly parses `2006-01` (`internal/store/store.go:753`). Phone Reports show blackbox incidents, not periodic health (`settings.js:306–385`); station reports do not fulfill the phone UI requirement.

## D. Mental health

| Item | Status | Evidence / gap |
|---|---|---|
| Mood | IMPLEMENTED | Three button options, persisted latest check-in and daily history (`log.js:130–131,183–189`; `link.js:268–279`). |
| Energy and fatigue | PARTIAL | Three energy buttons (`log.js:133–134`); fatigue inferred from mood/energy/sleep (`risk.js:184–190`). No separate fatigue question. |
| Stress and anxiety | MISSING | No questions. “No signs of fatigue or stress” is inferred from other fields (`diagnosis.js:12,203–210`). |
| Interest/apathy | MISSING | No field/section; check-in has only mood, energy, sleepMin, at (`log.js:185`). |
| Attention | MISSING | Same three-field check-in limitation. |
| Team and connection | MISSING | Crew selector/buddy alerts are operational features, not this assessment. |
| Loneliness and homesickness | MISSING | No field/storage/section. |
| Motivation and morale | MISSING | No field/storage/section. |
| Sleep | PARTIAL | Duration, timestamp, recommendation/warning (`log.js:136–143,185`; `diagnosis.js:262–268`); no quality/disturbance questions. |
| Personal-safety question | MISSING | No question/private safety workflow. Station “Need help” is an alert response (`link.js:160–161`), not the required question. |

Local risk never expires old check-ins (`risk.js:166–191,296–301`). Diagnosis does not mark example check-ins, unlike Log (`log.js:127–128`; `diagnosis.js:237–273`).

## E. Optional private menstrual-cycle module

| Item | Status | Evidence |
|---|---|---|
| Optional private module / enable-disable | MISSING | No phone cycle model, storage key, settings entry or view found. |
| Button-based logging | MISSING | No cycle controls. |
| Symptom tracking | MISSING | No symptoms/history. |
| Interpretation adjustments | MISSING | Station accepts cal.temp/cal.hr (`internal/model/model.go:17–18,42`; `internal/engine/engine.go:332–334`), but phone whitelist omits cal (`link.js:7,311–313`). No local adjustment. Backend luteal-phase comment (`internal/engine/rules.go:16–17`) is not an implementation of the phone module. |

## F. BLE

| Item | Status | Evidence / gap |
|---|---|---|
| Modular architecture | PARTIAL | Separate adapter/ingest boundary (`ble.js:17–27,55`; `telemetry.js:366–379`), but three hardcoded profiles; no registry, device ownership or measurement metadata. |
| Scan | BROKEN | requestDevice service chooser exists (`ble.js:29–35`), but only UI caller requires `[data-dx-pair-ble]` (`diagnosis.js:640–641`), never rendered. |
| Pair | BROKEN | Unreachable UI. Additionally failed tasks resolve null; Promise.all always returns length 3, so `if(!chars.length)` cannot detect all failures (`ble.js:37–49`). Unsupported device reports “BLE sensor paired · live readings enabled.” |
| List connected devices | MISSING | Internal links array has no rendered list; public API returns only boolean (`ble.js:44,55`). |
| Read data | PARTIAL | HR/thermometer/continuous PLX notifications (`ble.js:17–40`); no BP/HRV/RR/glucose/scale/dosimeter/sleep adapter, initial read or PLX spot-check path. Pairing unreachable. |
| Detect disconnection | BROKEN | Listener exists but clears all three channels regardless of device, never removes links entry, and connected() stays true (`ble.js:44–55`). clearHardware does not populate sim.lost (`telemetry.js:338–342,375–379`). |
| Low battery | MISSING | No battery service/characteristic or sensor battery field (`ble.js:6,34,37–40`). Simulated suit battery is unrelated. |
| Invalid readings | PARTIAL | Finite gate (`ble.js:23`) and HR/SpO2/temp plausibility checks (`risk.js:254–263`). No decoder length/status/unit/special-value checks (`ble.js:7–15,38–40`), nor catch around notification decoding. Invalid numbers can still display with direct risk colors (`log.js:334–341`). |
| Reconnect | MISSING | No hardware retry/reconnect controls (`ble.js:29–55`); telemetry.connect reconnects a simulation (`telemetry.js:274–303`). |
| Origin of each reading | BROKEN | Device name returned but not attached to readings (`ble.js:23,50`); any hardware value marks entire packet device (`telemetry.js:362`) while other metrics remain simulated. |
| No invented UUIDs/proprietary protocols | IMPLEMENTED | Named GATT services/characteristics, no custom UUID or proprietary format (`ble.js:6,38–40`). This does not establish decoder correctness or actual compatibility. |

Launcher supports TLS flags (`cmd/eva-station/main.go:40–41`); BLE reports unavailable browser environments (`ble.js:30–31`). Hardware/browser compatibility was not exercised.

## G. Offline and privacy

| Item | Status | Evidence / limitation |
|---|---|---|
| Storage survives closing/reopening | BROKEN for manual health; PARTIAL overall | PERSIST state and blackbox/outbox survive normal same-origin reloads; physicalManual does not. Storage errors silently fall back to memory (`core.js:27–47`), without durability warning. |
| Reopen app offline | PARTIAL | Already-loaded app works locally; source could be separately bundled. No service worker/cache manifest/offline shell in web/app. Server sends no-cache (`internal/server/server.go:112–115`); ordinary browser caching is not reliable cold-reopen support. External Google font URLs: `web/app/index.html:19–21`. |
| Later Station sync | PARTIAL | Persistent telemetry queue/retries/batches/commit ACKs (`link.js:16–24,71–100,229–247,309–318`; `internal/server/server.go:198–206,255–258`). Only link-enabled samples queued. Drops oldest after 20,000 (~5.6 h at 1 Hz, less with extra events). No manual sync; monthly path broken. Only today's readiness sent on reconnect, not previous offline days (`link.js:281–287,397`). |
| Private data stays on phone | BROKEN against literal brief | Raw mood/energy/profile objects excluded from telemetry, but readiness sends exact sleepH/hydration/fatigueHigh and BEHAVIORAL_LOAD derived from low mood (`link.js:298–307`); station persists them (`internal/store/store.go:478–487`). UI promises “Your mood, energy and notes stay on this phone” (`link.js:328`) without explaining derived disclosures or a sharing preference. |

Same-origin users can select all configured crew profiles/check-ins; no per-person privacy boundary (`core.js:86–94`; `app.js:135–152`). Explicit blackbox export contains check-in text and identity/baselines (`blackbox.js:130–133,251–269`), without private-field filtering. Refreshed station welcome now sends public identity only (`internal/engine/engine.go:931–939`); the prior full-medical roster is not reported as current behavior.

### Storage inventory

Prefix: `eva.` (`config.js:131`). Records are browser-origin localStorage, not filesystem data.

| Key | Survives normal reload? | Content / retention |
|---|---|---|
| eva.theme | Yes | Theme. |
| eva.roles | Yes | Crew roles; station roster can overwrite them. |
| eva.checkins | Yes | Latest mood/energy/sleepMin/at per crew; seeds have example:true. |
| eva.profiles | Yes | Name/initials/age/sex/specialty overrides, not comprehensive medical data. |
| eva.selectedCrewId | Yes | Displayed crew, distinct from phone link identity. |
| eva.voiceRate | Yes | Speech rate. |
| eva.linkEnabled | Yes | Link setting; defaults true when served under /app. |
| eva.linkCrewId | Yes | Station identity, default AST-001. |
| eva.stationHost | Yes | Separate-host setting; same-origin host takes precedence when station-served. |
| eva.dailyLog | Yes | Crew/day mood/energy/sleepMin/hydration; overwritten within a day, pruned to 30 dates on save. No precise time in derived row. |
| eva.outbox | Yes | Up to 20,000 compact rows: crew/seq/t, 13 numeric fields, source; ACK-pruned, not clinical history. |
| eva.linkSeq | Yes | Transmission sequence. |
| eva.monthlyOutbox | Yes if populated | Month + clinical values, no crew ID; valid API/UI cannot currently populate it. |
| eva.blackbox | Yes | Max 4,000 events; saves at most every 2 s, flushes on pagehide/hidden. Telemetry snapshots every 5 s; no 7-day guarantee. |
| physicalManual | **No** | In-memory only, neither PERSIST-listed nor restored. |
| view / voiceOn / evaStart / stationMission | No via PERSIST | View defaults Log (URL may route elsewhere); voice off; start resets until station update; mission received again. |
| BLE links, hardware values, histories, risk tracks, alerts, unsaved drafts | No | Runtime memory; simulations/history restart. |

Exact PERSIST (`core.js:54`): `['theme','roles','checkins','profiles','selectedCrewId','voiceRate','linkEnabled','linkCrewId','stationHost','dailyLog']`. Restore/write: `core.js:56–80`. Independently managed keys: `link.js:11–39,312`; `blackbox.js:7–28,89–92,175–181,199`. Any localStorage failure invalidates the Yes guarantees. A failed overwrite can also make storage.get return older localStorage before newer memory fallback (`core.js:30–41`).

## Phone ↔ station protocol

No type-name mismatch was found among emitted messages. Defects concern omitted fields, identity, provenance and semantics.

| Direction/type | Phone | Station counterpart |
|---|---|---|
| → hello | crewId/device (`link.js:118`) | Known-ID check, registration, welcome (`internal/server/server.go:162–185`); no ownership proof. |
| ← welcome | Mission/roles/alerts; queue replay (`link.js:229–235`) | Public roster, mission, serverTime, own high alerts (`internal/engine/engine.go:922–939`). Phone ignores serverTime and identity changes beyond roles. |
| → telemetry | crewId/seq/t/source + FIELDS (`link.js:7,309–318`) | Sample, crew rebound to connection (`internal/server/server.go:198–203`; `internal/model/model.go:24–43`). Valid source honored (`internal/engine/engine.go:238–247`). |
| → batch; ← batch-ok, ack-seq | 300-item batches; length check; prune only sent+ACKed (`link.js:72–87,236–247`) | Accepted count and separate database-commit ACK (`internal/server/server.go:204–206,255–258`). Correctly distinguishes acceptance from commit. |
| → queue | n every 10 s (`link.js:71,369`) | Queued count (`internal/server/server.go:207–208`); includes other-crew rows although replay selects current crew. |
| → readiness; ← readiness-ok | day/status/vulnerability/summary/factors/sleepH/hydration/fatigueHigh; success ignored (`link.js:281–307,397,261`) | Accepted/stored (`internal/server/server.go:221–230`; `internal/engine/engine.go:847–878`); no historical resend outbox. |
| → monthly-health; ← monthly-health-ok | Month + seven values (`link.js:40–54,241–242`) | Supported/persisted (`internal/server/server.go:209–220`; `internal/store/store.go:749–775`); phone UI/regex broken. |
| → ack | Numeric alertId + COPIED/IM_OK/NEED_HELP (`link.js:387–391`) | Matching enum (`internal/server/server.go:231–234`; `internal/engine/engine.go:558–565`); no connection-crew ownership check. |
| → incident | Online-only, no body (`link.js:393–395`) | Connection crew incident (`internal/server/server.go:235–236`); phone has no positive confirmation. |
| ← crew, mission | Roles/remote vitals/active start (`link.js:135–149,248–249`) | Tick/mission broadcasts (`internal/engine/engine.go:672–673,790–795,1259–1268`); phone ignores other status and identity fields. |
| ← alert, alert-update, buddy, instruction, abort | Render/speak/ACK (`link.js:151–225,250–256`) | Matching emitters (`internal/engine/engine.go:507–553,597–604,648–650,720,815`). Alert protocol array sent but not displayed; reason/voice only. |
| ← inject, clear, replaced, error | Simulate/clear, disable replaced phone, toast (`link.js:257–260`) | Matching handlers (`internal/server/server.go:430–474`; `internal/server/hub.go:153`). Replaced phone returns to local simulation. |
| → ping; ← pong | No ping sent; pong intentionally ignored (`link.js:261`) | Supported (`internal/server/server.go:237–238`); queue messages provide ongoing traffic. Not a type defect. |

### Field/behavior inconsistencies

1. **Wrong-person monthly risk, masked by current failures:** UI reads selected st.memberId (`diagnosis.js:643`), but queue has no crew ID and deduplicates only by month (`link.js:47–53`); server uses connection identity (`internal/server/server.go:212`). Changing selection/phone identity can attribute a panel to the wrong person once sync is enabled.
2. **BP source/age:** packets include simulated bpSys/bpDia, not manual cuff data or bpAt (`link.js:7,313`; `telemetry.js:107–110,359–362`). Station retains BP when missing (`internal/engine/engine.go:341–343`). Remote phone assigns `bpAt=vitals.t` (`telemetry.js:484`), falsely refreshing old BP.
3. **Calibration/resting baselines omitted:** station supports cal/hrRest/hrvRest (`internal/model/model.go:42,114–115`), phone sends none (`link.js:7,304–307`). Station adaptive baseline uses resting history (`internal/engine/engine.go:1127–1174`); phone neither supplies it nor consumes the resulting baseline.
4. **Remote provenance dropped:** ingest copies numeric fields but not source (`telemetry.js:480–485`). isHardwareConnected only checks local sims (`telemetry.js:328`); remote hardware labels as demo. Mixed local packets conversely label as device.
5. **Mission/roster divergence:** stationMission has no reader; Profile/Settings/exports use config (`profile.js:149–151`; `settings.js:145–164`; `blackbox.js:254`). Names ignored; only roles applied (`link.js:142–149`). Local role edits are overwritten by station tick.
6. **Readiness disconnected from health inputs:** Log never saves hydration (`log.js:185`); readiness defaults it to 3 (`link.js:274,300`). Physical sleep/hydration/glucose etc. cannot affect readiness. Previous offline dates and resting HR/HRV are not transmitted.
7. **Clock mismatch:** serverTime ignored; phone timestamps from local clock. Station clamps invalid/future live times but rejects corresponding batch rows (`internal/engine/engine.go:255–256,284–285`). A future-dated offline row can repeatedly force partial-batch reconnects (`link.js:246`).
8. **Virtual data not isolated:** station honors virtual source but still evaluates/accumulates dose (`internal/engine/engine.go:238–247,321–355`). Console device source derives from connection presence (`internal/engine/engine.go:1238–1242`), not sample origin.
9. **Device-channel trust:** hello needs only a known crew ID (`internal/server/server.go:169–183`); ACK passes no connection crew (`internal/server/server.go:232`). New operator guards do not fix phone identity/alert ownership. Source finding only, no live exploit performed.

## Duplicated logic and dead/unreachable code

| Finding | Evidence / consequence |
|---|---|
| Three physical presentations | Log, diagnosis table, daily cards (`log.js:213–258`; `diagnosis.js:317–407,434–531`) differ in values/source/time/risk. |
| Multiple classifiers | Config/risk (`config.js:73–96`; `risk.js:115–191,243–276`), direct Log classification (`log.js:218,334`), card prose (`diagnosis.js:454–470`), Go rules (`internal/engine/rules.go:131–179`). Filtering, effort, baseline and thresholds diverge. |
| Mental/readiness duplication | Diagnosis fallback (`diagnosis.js:203–210`), risk (`risk.js:173–191`), link readiness (`link.js:289–307`), station vulnerability (`internal/engine/engine.go:997` onward); different sleep cutoffs/escalation. |
| Identity/baselines/simulation duplicated | Phone crew (`config.js:25–61`) versus station seeds (`internal/store/seed.go:24–38`); phone and Go simulators (`telemetry.js:25–44,130–198`; `internal/sim/sim.go`). No shared schema/rule source. |
| Date-only parsing duplicated | profile localDate (`profile.js:50–54`) before formatter already supporting YYYY-MM-DD (`core.js:201–206`). |
| Unrendered labs | MONTHLY_LABS defined/updated but never initially rendered (`diagnosis.js:449–452,498,505`). |
| Unreachable BLE/monthly actions | Data attributes occur only as click selectors (`diagnosis.js:640–643`); no buttons. BLE connected() has no UI consumer (`ble.js:55`). |
| Obsolete BP action | measure-bp handler (`log.js:422–437,451`) has no button; current BP button navigates to diagnosis (`log.js:256–257`). Simulator API remains (`telemetry.js:389–427`). |
| Unused config/state | mental.hydration (`config.js:107`) never rendered; stationMission never read (`link.js:137`); SENSOR_KEYS.rad / PHYSICAL_GUIDANCE.rad (`diagnosis.js:448,466`) have no daily card, which uses radiationMonth. |
| BP render cache never updated | lastBpAt initializes/resets null (`log.js:16,473`) but never receives rendered time; `log.js:367` repeatedly redraws BP. |

## Demo data mistaken for real — BROKEN

- **Auto simulation:** `app.js:295` starts telemetry; auto-connect and generated warmup history (`telemetry.js:213–219`) require no demo opt-in. Some labels correctly say demo (`log.js:220`; `diagnosis.js:498`), but coverage is inconsistent.
- **Disclaimer disappears:** suit tile initially says “Demo data · hardware not paired” (`log.js:290`), then tick replaces it with normal/risk text (`log.js:359`). Header says “Live from the suit” (`log.js:266`).
- **Mixed packet marked real:** `source = Object.keys(sim.hardware).length ? 'device' : 'virtual'` (`telemetry.js:362`); one real channel can make all generated channels travel as device.
- **Fabricated hardware charts:** history uses generated value (`telemetry.js:196`) while display uses hardware (`telemetry.js:357`), without chart-specific disclosure.
- **Invented earlier BP:** random values timestamped 20 minutes ago (`telemetry.js:107–110`) appear as “Now” in Profile (`profile.js:87–93`) and are transmitted, while Log asks for manual BP.
- **Unmarked profile examples:** config says “Values are examples” (`config.js:1`), but Profile presents medical history and dated “Measured on” baselines (`profile.js:158–186`) without sample labels.
- **Example mental check-ins:** seeds persisted with example:true (`risk.js:346–355`); Log labels them, diagnosis/risk do not. Saving prefilled form without changes drops example flag (`log.js:185,480–484`), allowing example values into readiness.
- **Risk/speech/export provenance loss:** local risk evaluates demos (`risk.js:243–276`). Blackbox snapshots omit source, RR, HRV and radiation (`blackbox.js:72`); CSV has no source column (`blackbox.js:266–269`). Incident reports cannot distinguish demo/real. Station seeded monthly panels lack per-row sample flags (`internal/store/seed.go:129–147`; `internal/model/model.go:198–214`).
- **Unsupported reassurance:** “No signs of fatigue or stress” uses only mood/energy/sleep (`diagnosis.js:12,203–210`), including examples/stale check-ins.

## Verification

All 15 phone JavaScript files parsed with Node vm.Script (syntax only). VM execution used mock storage/timers/BLE; no test files/fixtures/log files created.

| Probe | Observed result |
|---|---|
| Save manual health/check-in, recreate core with same storage | physicalManual:null; check-in restored; only eva.checkins written. |
| sendMonthlyHealth({month:'2026-11',glucose:99}) | Throws Monthly panel requires YYYY-MM. |
| Inject HR=123 hardware, advance tick | Current 123, source device, but last HR chart sample generated (~67.8), 60 points. |
| All mocked GATT service lookups fail | Pair resolves with success toast; connected() true. |
| Fire GATT disconnection | connected() still true. |
| Render IV diagnosis with real renderer/minimal DOM stub | 12 physical cards; no pairing button, monthly-sync button or hemoglobin card. Static search found no alternate controls. |

No browser layout test, real BLE test, station launch, database write, Go build or live network integration test. MISSING means no implementation found in audited phone files; a station-only similarly named feature does not satisfy the phone requirement.

## Prioritized work

S = focused change; M = several interacting modules; L = feature/data-model work plus end-to-end validation. Relative sizes, not calendar estimates.

| Priority | Size | Work |
|---|---|---|
| P0 critical | M | Explicit demo mode and per-metric provenance through UI/history/risk/wire/station/reports/exports; stop mixed packets appearing wholly real and demo labels disappearing. |
| P0 critical | M | Persist/restore manual records with timestamp/unit/source/validation; surface storage failure and prevent silent reload/quota loss. |
| P0 critical | M | Hardware history must use acquired samples; add per-reading freshness/invalid state rather than simulator-renewed timestamps. |
| P0 critical | M | Unify manual/sensor/demo BP, RR and sleep across Log/diagnosis/risk/station; remove invented BP fallback and align displayed/computed rules. |
| P0 critical | M | Enforce privacy contract: remove/private-gate exact sleep and behavioral/fatigue disclosures; define permitted summaries and private export exclusions. |
| P0 critical | M | Bind device identity/ACKs to authorized crew and monthly rows to crew before enabling monthly sync. |
| P1 | M | Render BLE controls/device list; fix false pairing, disconnect ownership, decoder flags/validation, reconnect and sensor battery monitoring; test actual supported hardware. |
| P1 | L | Timestamped histories/7-day charts, baselines, lights, sources/status/instructions for all 12; add sleep quality, fasting metadata and full orthostatic measurements. |
| P1 | M | Render labs/monthly panels, fix regex, add crew/month identity, weight-change/exercise tracking, history and reliable ACK/retry. |
| P1 | L | Missing mental sections and personal-safety workflow; label examples/stale check-ins, remove unsupported reassuring conclusions. |
| P1 | M | Complete profile capture for medical history/allergies/medications/supplements/blood type/height/weight/contacts; show crew ID/duration and synchronize mission/profile mapping. |
| P1 | M | Reliable offline shell, durable histories/outboxes, capacity warnings, prior-day replay and clock-skew handling. |
| P2 | L | Optional private menstrual logging/symptoms and justified interpretation adjustments; define any allowed calibration-only protocol. |
| P2 | M | Consolidate duplicated data/rules/configuration, remove dead handlers/state and consume station mission/roster/baselines consistently. |
| P2 | S | Fix repetitive BP redraw/incomplete blackbox exports; add focused regression checks for reproduced failures. |
