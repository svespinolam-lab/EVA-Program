package main

import (
 "database/sql"
 "crypto/subtle"
 "strings"
 "encoding/json"
 "log"
 "net/http"
 "os"
 "sort"
 "strconv"
 "sync"
 "time"

 "github.com/gorilla/websocket"
 _ "modernc.org/sqlite"
)

type Reading struct {
 EventID string `json:"event_id"`
 MissionID string `json:"mission_id"`
 AstronautID string `json:"astronauta_id"`
 Timestamp string `json:"timestamp"`
 HR float64 `json:"fc_lpm"`
 SpO2 float64 `json:"spo2_pct"`
 Temp float64 `json:"temp_c"`
 Resp float64 `json:"frec_resp_rpm"`
 HRV float64 `json:"hrv_ms"`
 Radiation float64 `json:"radiacion_eva_msv"`
 Effort string `json:"nivel_esfuerzo"`
 HRThresholdOffset float64 `json:"hr_threshold_offset_lpm"`
 TempThresholdOffset float64 `json:"temp_threshold_offset_c"`
}
type Status struct { Reading; AlertID int64 `json:"alert_id"`; Score int `json:"ipv_score"`; Level string `json:"semaforo"`; Reason string `json:"diagnostico"`; Protocol string `json:"protocolo"` }
type SafeCheck struct { AstronautID string `json:"astronauta_id"`; MissionID string `json:"mision_id"`; TrafficLight string `json:"semaforo_go_nogo"`; Vulnerability int `json:"indice_vulnerabilidad"`; Summary string `json:"resumen_seguro_pc"`; Timestamp string `json:"timestamp"` }

var db *sql.DB
var mu sync.Mutex
var clients = map[*websocket.Conn]bool{}
var latest = map[string]Status{}
var upgrader=websocket.Upgrader{CheckOrigin:func(r *http.Request)bool{return true}}

func initDB(){
 var err error
 path:=os.Getenv("EVA_DB"); if path=="" {path="eva_station.db"}
 db,err=sql.Open("sqlite",path); if err!=nil {log.Fatal(err)}
 db.SetMaxOpenConns(1)
 schema:=`
 PRAGMA journal_mode=WAL;
 CREATE TABLE IF NOT EXISTS astronauts(id TEXT PRIMARY KEY,name TEXT NOT NULL,role TEXT,sex TEXT,base_hr REAL DEFAULT 70,base_spo2 REAL DEFAULT 98,base_temp REAL DEFAULT 36.8,base_hrv REAL DEFAULT 50,radiation_total REAL DEFAULT 0,vulnerability INTEGER DEFAULT 0, vulnerability_reason TEXT DEFAULT '');
 CREATE TABLE IF NOT EXISTS missions(id TEXT PRIMARY KEY,name TEXT,environment TEXT,start_at TEXT,end_at TEXT,status TEXT DEFAULT 'ACTIVE');
 CREATE TABLE IF NOT EXISTS telemetry(id INTEGER PRIMARY KEY AUTOINCREMENT,event_id TEXT UNIQUE,mission_id TEXT,astronaut_id TEXT,ts TEXT,hr REAL,spo2 REAL,temp REAL,resp REAL,hrv REAL,radiation REAL,effort TEXT,ipv INTEGER,level TEXT,reason TEXT,protocol TEXT);
 CREATE INDEX IF NOT EXISTS telemetry_lookup ON telemetry(mission_id,astronaut_id,ts);
 CREATE TABLE IF NOT EXISTS alerts(id INTEGER PRIMARY KEY AUTOINCREMENT,mission_id TEXT,astronaut_id TEXT,ts TEXT,level TEXT,code TEXT,message TEXT,reason TEXT,protocol TEXT,response TEXT DEFAULT 'PENDING',response_seconds INTEGER,escalated INTEGER DEFAULT 0);
 CREATE TABLE IF NOT EXISTS incidents(id INTEGER PRIMARY KEY AUTOINCREMENT,mission_id TEXT,astronaut_id TEXT,alert_id INTEGER,event_at TEXT,window_start TEXT,window_end TEXT,classification TEXT,first_deviation TEXT,readings_json TEXT);
 CREATE TABLE IF NOT EXISTS premission_checks(id INTEGER PRIMARY KEY AUTOINCREMENT,astronaut_id TEXT,mission_id TEXT,ts TEXT,traffic_light TEXT,vulnerability INTEGER,summary TEXT);
 CREATE TABLE IF NOT EXISTS monthly_reports(id INTEGER PRIMARY KEY AUTOINCREMENT,astronaut_id TEXT,month TEXT,payload TEXT,UNIQUE(astronaut_id,month));
 `
 if _,err=db.Exec(schema);err!=nil{log.Fatal(err)}
 db.Exec("ALTER TABLE telemetry ADD COLUMN event_id TEXT") // harmless when the column already exists
 db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_telemetry_event_id ON telemetry(event_id)")
 // Demo roster. Replace these records with authenticated crew provisioning before operational use.
 db.Exec(`INSERT OR IGNORE INTO astronauts(id,name,role,sex,base_hr,base_spo2,base_temp,base_hrv) VALUES
 ('AST-001','Crew Member 01','EXPLORER','F',70,98,36.8,50),
 ('AST-002','Crew Member 02','EXPLORER','M',68,98,36.7,52),
 ('AST-003','Crew Member 03','EXPLORER','M',72,97,36.8,48)`)
 db.Exec(`INSERT OR IGNORE INTO astronauts(id,name,role,sex,base_hr,base_spo2,base_temp,base_hrv) VALUES('ORB-03-114','Andrea Salinas','EXPLORER','F',70,98,36.8,50)`)
 db.Exec(`INSERT OR IGNORE INTO missions(id,name,environment,start_at,status) VALUES('EVA-2026-09','Lunar Surface EVA','LUNAR','', 'ACTIVE')`)
}
func evaluate(r Reading) Status {
 var bHR,bO2,bT,bHRV float64=70,98,36.8,50
 var vuln int
 db.QueryRow("SELECT base_hr,base_spo2,base_temp,base_hrv,vulnerability FROM astronauts WHERE id=?",r.AstronautID).Scan(&bHR,&bO2,&bT,&bHRV,&vuln)
 // Personal baseline: first 14 days establish adaptation; afterwards use a rolling 7-day window.
 var samples int
 db.QueryRow("SELECT COUNT(DISTINCT date(ts)) FROM telemetry WHERE astronaut_id=?",r.AstronautID).Scan(&samples)
 window:="-14 days"
 if samples>=15 { window="-7 days" }
 var n int
 row:=db.QueryRow("SELECT COUNT(*),AVG(hr),AVG(spo2),AVG(temp),AVG(NULLIF(hrv,0)) FROM telemetry WHERE astronaut_id=? AND ts>=datetime('now',?)",r.AstronautID,window)
 var avgHR,avgO2,avgT,avgHRV sql.NullFloat64
 row.Scan(&n,&avgHR,&avgO2,&avgT,&avgHRV)
 if samples>=14 && n>=5 { if avgHR.Valid {bHR=avgHR.Float64};if avgO2.Valid {bO2=avgO2.Float64};if avgT.Valid {bT=avgT.Float64};if avgHRV.Valid {bHRV=avgHRV.Float64} }
 score:=vuln; reasons:=[]string{}; protocol:="Continue standard monitoring."
 effectiveHR:=r.HR-r.HRThresholdOffset;effectiveTemp:=r.Temp-r.TempThresholdOffset
 if r.SpO2<90 {score=max(score,98);reasons=append(reasons,"Critical oxygen saturation");protocol="Stop work. Check suit oxygen. Return to airlock."} else if r.SpO2<=92 {score=max(score,78);reasons=append(reasons,"Low oxygen saturation");protocol="Reduce workload and verify oxygen supply."}
 if effectiveTemp>=39 {score=max(score,95);reasons=append(reasons,"Critical temperature");protocol="Abort EVA and activate cooling protocol."} else if effectiveTemp>=38 {score=max(score,74);reasons=append(reasons,"Elevated temperature")}
 effort:=r.Effort;if effort==""{effort="MODERATE"}
 limit:=bHR+15;if effort=="REST"{limit=bHR*1.35};if effort=="MODERATE"{limit=bHR+30};if effort=="HIGH"{limit=bHR+55}
 if effectiveHR>165 || (effectiveHR>130&&effort=="REST"){score=max(score,93);reasons=append(reasons,"Severe heart-rate elevation");protocol="Stop activity and assess crew member."}else if effectiveHR>limit{score=max(score,73);reasons=append(reasons,"Heart rate above personal baseline")}
 if bHRV>0&&r.HRV>0&&r.HRV<bHRV*.65{score=min(100,score+12);reasons=append(reasons,"HRV decreased significantly")}
 level:="STABLE";if score>=90{level="EMERGENCY"}else if score>=70{level="RISK"}else if score>=40{level="VULNERABLE"}
 reason:="Within monitored parameters";if len(reasons)>0{reason=reasons[0]}
 r.Timestamp=time.Now().UTC().Format(time.RFC3339)
 return Status{Reading:r,Score:score,Level:level,Reason:reason,Protocol:protocol}
}
func broadcast(v any){mu.Lock();defer mu.Unlock();for c:=range clients{c.SetWriteDeadline(time.Now().Add(2*time.Second));if err:=c.WriteJSON(v);err!=nil{c.Close();delete(clients,c)}}}
func record(r Reading) (Status,error){
 if r.AstronautID==""||r.MissionID==""||r.HR<=0||r.SpO2<=0||r.Temp<=0{return Status{},http.ErrBodyNotAllowed}
 if r.EventID==""{r.EventID=r.AstronautID+"-"+time.Now().UTC().Format("20060102150405.000000000")}
 s:=evaluate(r);res,err:=db.Exec(`INSERT OR IGNORE INTO telemetry(event_id,mission_id,astronaut_id,ts,hr,spo2,temp,resp,hrv,radiation,effort,ipv,level,reason,protocol) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,r.EventID,r.MissionID,r.AstronautID,s.Timestamp,r.HR,r.SpO2,r.Temp,r.Resp,r.HRV,r.Radiation,r.Effort,s.Score,s.Level,s.Reason,s.Protocol)
 if err!=nil{return s,err};affected,_:=res.RowsAffected();if affected==0{return s,nil}
 mu.Lock();previous,hadPrevious:=latest[r.AstronautID];latest[r.AstronautID]=s;mu.Unlock()
 if (s.Level=="RISK"||s.Level=="EMERGENCY")&&(!hadPrevious||previous.Level!=s.Level){
  msg:="Check vital signs and follow crew protocol.";if s.Level=="EMERGENCY"{msg="Critical vital signs. Stop EVA and return to airlock."}
  res,e:=db.Exec("INSERT INTO alerts(mission_id,astronaut_id,ts,level,code,message,reason,protocol) VALUES(?,?,?,?,?,?,?,?)",r.MissionID,r.AstronautID,s.Timestamp,s.Level,"VITALS_"+s.Level,msg,s.Reason,s.Protocol)
  if e==nil {id,_:=res.LastInsertId();s.AlertID=id;mu.Lock();latest[r.AstronautID]=s;mu.Unlock();if s.Level=="EMERGENCY"{go escalate(id,r.AstronautID,r.MissionID);go captureIncident(id,r.MissionID,r.AstronautID,time.Now().UTC())}}
 }
 return s,nil
}
func escalate(alertID int64,astro,mission string){time.Sleep(30*time.Second);var response string;err:=db.QueryRow("SELECT response FROM alerts WHERE id=?",alertID).Scan(&response);if err==nil&&response=="PENDING"{db.Exec("UPDATE alerts SET escalated=1 WHERE id=?",alertID);broadcast(map[string]any{"type":"ESCALATION","alert_id":alertID,"astronauta_id":astro,"mision_id":mission,"message":"Unacknowledged emergency. Notify the other crew member immediately."})}}
func ws(w http.ResponseWriter,r *http.Request){c,e:=upgrader.Upgrade(w,r,nil);if e!=nil{return};mu.Lock();clients[c]=true;mu.Unlock();defer func(){mu.Lock();delete(clients,c);mu.Unlock();c.Close()}()
 mu.Lock();rank:=make([]Status,0,len(latest));for _,v:=range latest{rank=append(rank,v)};mu.Unlock();c.WriteJSON(map[string]any{"type":"RANKING_UPDATED","astronautas":rank})
 for{var msg map[string]json.RawMessage;if e:=c.ReadJSON(&msg);e!=nil{return};if raw,ok:=msg["type"];ok{var typ string;json.Unmarshal(raw,&typ);if typ=="ACK"{var id int64;json.Unmarshal(msg["alert_id"],&id);var response string;json.Unmarshal(msg["response"],&response);db.Exec("UPDATE alerts SET response=?,response_seconds=CAST((julianday('now')-julianday(ts))*86400 AS INTEGER) WHERE id=?",response,id);broadcast(map[string]any{"type":"ACK_RECEIVED","alert_id":id,"response":response})};continue};var r Reading;b,_:=json.Marshal(msg);if json.Unmarshal(b,&r)==nil{s,err:=record(r);if err==nil{broadcast(map[string]any{"type":"RANKING_UPDATED","astronautas":ranking(),"latest":s})}}}
}
func ranking()[]Status{mu.Lock();defer mu.Unlock();a:=make([]Status,0,len(latest));for _,s:=range latest{a=append(a,s)};sort.Slice(a,func(i,j int)bool{return a[i].Score>a[j].Score});return a}

func captureIncident(alertID int64, mission, astronaut string, event time.Time) {
 time.Sleep(15*time.Minute)
 start:=event.Add(-30*time.Minute).Format(time.RFC3339)
 end:=event.Add(15*time.Minute).Format(time.RFC3339)
 rows,err:=db.Query("SELECT ts,hr,spo2,temp,resp,hrv,radiation,effort,ipv,level FROM telemetry WHERE mission_id=? AND astronaut_id=? AND ts BETWEEN ? AND ? ORDER BY ts",mission,astronaut,start,end)
 if err!=nil{return};defer rows.Close()
 samples:=[]map[string]any{};first:=""
 for rows.Next(){var ts,effort,level string;var hr,spo2,temp,resp,hrv,rad float64;var ipv int
  if rows.Scan(&ts,&hr,&spo2,&temp,&resp,&hrv,&rad,&effort,&ipv,&level)==nil{
   samples=append(samples,map[string]any{"timestamp":ts,"hr":hr,"spo2":spo2,"temp":temp,"resp":resp,"hrv":hrv,"radiation":rad,"effort":effort,"ipv":ipv,"level":level})
   if first==""&&ipv>=70{first=ts}
  }
 }
 data,_:=json.Marshal(samples);class:="SUDDEN"
 var vuln int;db.QueryRow("SELECT vulnerability FROM astronauts WHERE id=?",astronaut).Scan(&vuln);if vuln>=30{class="PROGRESSIVE"}
 for _,s:=range samples{if s["spo2"].(float64)<=0||s["hr"].(float64)<=0||s["temp"].(float64)<25||s["temp"].(float64)>45{class="SENSOR_FAILURE";break}}
 if first==""{first=event.Format(time.RFC3339)}
 db.Exec("INSERT INTO incidents(mission_id,astronaut_id,alert_id,event_at,window_start,window_end,classification,first_deviation,readings_json) VALUES(?,?,?,?,?,?,?,?,?)",mission,astronaut,alertID,event.Format(time.RFC3339),start,end,class,first,string(data))
}

func operatorAuth(r *http.Request) bool {
 expected:=os.Getenv("EVA_OPERATOR_TOKEN");if expected==""{return false}
 got:=strings.TrimPrefix(r.Header.Get("Authorization"),"Bearer ")
 return len(got)==len(expected)&&subtle.ConstantTimeCompare([]byte(got),[]byte(expected))==1
}

func main(){
 initDB()
 http.HandleFunc("/app/",http.StripPrefix("/app/",http.FileServer(http.Dir("."))))
 http.HandleFunc("/",func(w http.ResponseWriter,r *http.Request){if r.URL.Path!="/" {http.NotFound(w,r);return};http.ServeFile(w,r,"dashboard/index.html")})
 http.HandleFunc("/ws/monitor",ws)
 http.HandleFunc("/api/health",func(w http.ResponseWriter,r *http.Request){json.NewEncoder(w).Encode(map[string]any{"status":"ok","offline_first":true,"service":"EVA mission station"})})
 http.HandleFunc("/api/premission",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"POST required",405);return};var p SafeCheck;if json.NewDecoder(http.MaxBytesReader(w,r.Body,8192)).Decode(&p)!=nil||p.AstronautID==""||(p.TrafficLight!="GREEN"&&p.TrafficLight!="AMBER"&&p.TrafficLight!="RED"){http.Error(w,"Invalid safe pre-mission payload",400);return};p.Timestamp=time.Now().UTC().Format(time.RFC3339);db.Exec("UPDATE astronauts SET vulnerability=?,vulnerability_reason=? WHERE id=?",p.Vulnerability,p.Summary,p.AstronautID);_,e:=db.Exec("INSERT INTO premission_checks(astronaut_id,mission_id,ts,traffic_light,vulnerability,summary) VALUES(?,?,?,?,?,?)",p.AstronautID,p.MissionID,p.Timestamp,p.TrafficLight,p.Vulnerability,p.Summary);if e!=nil{http.Error(w,e.Error(),500);return};json.NewEncoder(w).Encode(map[string]any{"accepted":true,"private_diary_received":false})})
 http.HandleFunc("/api/telemetry",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"POST required",405);return};var x Reading;if json.NewDecoder(http.MaxBytesReader(w,r.Body,8192)).Decode(&x)!=nil{http.Error(w,"Invalid JSON",400);return};s,e:=record(x);if e!=nil{http.Error(w,e.Error(),400);return};broadcast(map[string]any{"type":"RANKING_UPDATED","astronautas":ranking(),"latest":s});json.NewEncoder(w).Encode(s)})
 http.HandleFunc("/api/triage",func(w http.ResponseWriter,r *http.Request){json.NewEncoder(w).Encode(ranking())})
 http.HandleFunc("/api/simulator",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"POST required",405);return};var x Reading;if json.NewDecoder(r.Body).Decode(&x)!=nil{http.Error(w,"Invalid JSON",400);return};s,e:=record(x);if e!=nil{http.Error(w,e.Error(),400);return};broadcast(map[string]any{"type":"RANKING_UPDATED","astronautas":ranking(),"latest":s});json.NewEncoder(w).Encode(s)})
 http.HandleFunc("/api/report/monthly",func(w http.ResponseWriter,r *http.Request){if !operatorAuth(r){http.Error(w,"Operator authorization required",401);return};if r.Method!="POST"{http.Error(w,"POST required",405);return};var p struct{AstronautID string `json:"astronauta_id"`;Month string `json:"month"`;Payload json.RawMessage `json:"payload"`};if json.NewDecoder(http.MaxBytesReader(w,r.Body,65536)).Decode(&p)!=nil||p.AstronautID==""||len(p.Payload)==0||len(p.Month)!=7{http.Error(w,"Invalid monthly report",400);return};_,e:=db.Exec("INSERT INTO monthly_reports(astronaut_id,month,payload) VALUES(?,?,?) ON CONFLICT(astronaut_id,month) DO UPDATE SET payload=excluded.payload",p.AstronautID,p.Month,string(p.Payload));if e!=nil{http.Error(w,e.Error(),500);return};json.NewEncoder(w).Encode(map[string]any{"saved":true})})
 http.HandleFunc("/api/report/monthly/",func(w http.ResponseWriter,r *http.Request){if !operatorAuth(r){http.Error(w,"Operator authorization required",401);return};astro:=strings.TrimPrefix(r.URL.Path,"/api/report/monthly/");month:=r.URL.Query().Get("month");var payload string;e:=db.QueryRow("SELECT payload FROM monthly_reports WHERE astronaut_id=? AND month=?",astro,month).Scan(&payload);if e!=nil{http.Error(w,"Monthly report not found",404);return};w.Header().Set("Content-Type","application/json");w.Write([]byte(payload))})
 http.HandleFunc("/api/incidents/",func(w http.ResponseWriter,r *http.Request){if !operatorAuth(r){http.Error(w,"Operator authorization required",401);return};mission:=r.URL.Path[len("/api/incidents/"):];rows,e:=db.Query("SELECT id,astronaut_id,event_at,window_start,window_end,classification,first_deviation FROM incidents WHERE mission_id=? ORDER BY event_at DESC",mission);if e!=nil{http.Error(w,e.Error(),500);return};defer rows.Close();out:=[]map[string]any{};for rows.Next(){var id int;var astro,at,start,end,kind,first string;rows.Scan(&id,&astro,&at,&start,&end,&kind,&first);out=append(out,map[string]any{"id":id,"astronaut_id":astro,"event_at":at,"window_start":start,"window_end":end,"classification":kind,"first_deviation":first})};json.NewEncoder(w).Encode(out)})
 http.HandleFunc("/api/report/mission/",func(w http.ResponseWriter,r *http.Request){if !operatorAuth(r){http.Error(w,"Operator authorization required",401);return};id:=r.URL.Path[len("/api/report/mission/"):];rows,e:=db.Query("SELECT astronaut_id,COUNT(*),ROUND(AVG(hr),1),MAX(hr),MIN(spo2),MAX(temp),MAX(ipv) FROM telemetry WHERE mission_id=? GROUP BY astronaut_id ORDER BY MAX(ipv) DESC",id);if e!=nil{http.Error(w,e.Error(),500);return};defer rows.Close();out:=[]map[string]any{};for rows.Next(){var a string;var n int;var avg,mx,lo,tm float64;var score int;rows.Scan(&a,&n,&avg,&mx,&lo,&tm,&score);out=append(out,map[string]any{"astronaut_id":a,"samples":n,"mean_hr":avg,"max_hr":mx,"min_spo2":lo,"max_temp":tm,"max_ipv":score})};json.NewEncoder(w).Encode(out)})
 port:=os.Getenv("EVA_PORT");if port==""{port="8000"}
 addr:="0.0.0.0:"+port
 cert,key:=os.Getenv("EVA_TLS_CERT"),os.Getenv("EVA_TLS_KEY")
 log.Printf("EVA local mission server listening on %s",addr)
 if cert!=""&&key!=""{log.Fatal(http.ListenAndServeTLS(addr,cert,key,nil))}
 log.Print("WARNING: HTTP mode disables Web Bluetooth and installable PWA on most phones. Configure trusted TLS certificates for field use.")
 log.Fatal(http.ListenAndServe(addr,nil))
}
var _ = strconv.Itoa
