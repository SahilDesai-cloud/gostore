package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	gostore "github.com/SahilDesai-cloud/gostore"
)

var db *gostore.DB

func main() {
	dir := "./gostore-data"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	var err error
	db, err = gostore.Open(dir, gostore.Options{BloomBitsPerKey: 10})
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	http.HandleFunc("/", handleUI)
	http.HandleFunc("/api/keys", handleAllKeys)
	http.HandleFunc("/api/keys/", handleKey)
	http.HandleFunc("/api/scan", handleScan)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	fmt.Printf("gostore running at http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// ── API handlers ──────────────────────────────────────────────────────────────

func handleAllKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	it, err := db.Scan(nil, nil)
	if err != nil {
		jsonError(w, err, 500)
		return
	}
	defer it.Close()
	var rows []map[string]string
	for it.Next() {
		rows = append(rows, map[string]string{
			"key":   string(it.Key()),
			"value": string(it.Value()),
		})
	}
	if it.Err() != nil {
		jsonError(w, it.Err(), 500)
		return
	}
	if rows == nil {
		rows = []map[string]string{}
	}
	jsonOK(w, rows)
}

func handleKey(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/api/keys/")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		v, found, err := db.Get([]byte(key))
		if err != nil {
			jsonError(w, err, 500)
			return
		}
		if !found {
			jsonError(w, fmt.Errorf("key %q not found", key), 404)
			return
		}
		jsonOK(w, map[string]string{"key": key, "value": string(v)})

	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			jsonError(w, err, 400)
			return
		}
		if err := db.Put([]byte(key), body); err != nil {
			jsonError(w, err, 500)
			return
		}
		jsonOK(w, map[string]string{"status": "ok", "key": key})

	case http.MethodDelete:
		if err := db.Delete([]byte(key)); err != nil {
			jsonError(w, err, 500)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted", "key": key})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var start, end []byte
	if s := r.URL.Query().Get("start"); s != "" {
		start = []byte(s)
	}
	if e := r.URL.Query().Get("end"); e != "" {
		end = []byte(e)
	}
	it, err := db.Scan(start, end)
	if err != nil {
		jsonError(w, err, 500)
		return
	}
	defer it.Close()
	var rows []map[string]string
	for it.Next() {
		rows = append(rows, map[string]string{
			"key":   string(it.Key()),
			"value": string(it.Value()),
		})
	}
	if it.Err() != nil {
		jsonError(w, it.Err(), 500)
		return
	}
	if rows == nil {
		rows = []map[string]string{}
	}
	jsonOK(w, rows)
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// ── UI ────────────────────────────────────────────────────────────────────────

func handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, ui)
}

const ui = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>gostore</title>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
:root{
  /* deep purple-black backgrounds */
  --bg0:#05050f;--bg1:#08081a;--bg2:#0d0d22;--bg3:#11112c;--bg4:#161638;
  /* violet-tinted borders */
  --b0:rgba(139,92,246,.06);--b1:rgba(139,92,246,.12);--b2:rgba(139,92,246,.22);
  /* primary: violet */
  --accent:#8b5cf6;--adim:rgba(139,92,246,.13);--aborder:rgba(139,92,246,.35);
  /* secondary: cyan */
  --cyan:#22d3ee;--cdim:rgba(34,211,238,.1);--cborder:rgba(34,211,238,.3);
  /* semantic */
  --green:#10b981;--gdim:rgba(16,185,129,.12);--gborder:rgba(16,185,129,.3);
  --blue:#38bdf8;--bdim:rgba(56,189,248,.12);--bborder:rgba(56,189,248,.3);
  --red:#f87171;--rdim:rgba(248,113,113,.12);--rborder:rgba(248,113,113,.3);
  --purple:#c084fc;--pink:#f472b6;
  /* text: violet-tinted */
  --t1:#ede9fe;--t2:#a78bfa;--t3:#4c3a8a;
  --mono:'JetBrains Mono','Cascadia Code','Fira Code',monospace;
  --grad:linear-gradient(135deg,#a855f7,#6366f1,#22d3ee);
  --grad2:linear-gradient(135deg,#7c3aed,#0ea5e9);
  --shadow:0 8px 40px rgba(0,0,0,.7);
}

body{
  background:var(--bg0);
  background-image:
    radial-gradient(ellipse 80% 50% at 60% -10%,rgba(99,102,241,.07) 0%,transparent 60%),
    radial-gradient(ellipse 60% 40% at 0% 100%,rgba(139,92,246,.05) 0%,transparent 50%);
  color:var(--t1);font-family:system-ui,-apple-system,sans-serif;
  height:100vh;display:flex;flex-direction:column;overflow:hidden;font-size:13px;
}

/* ── SCROLLBARS ── */
::-webkit-scrollbar{width:3px;height:3px}
::-webkit-scrollbar-track{background:transparent}
::-webkit-scrollbar-thumb{background:rgba(139,92,246,.3);border-radius:99px}
::-webkit-scrollbar-thumb:hover{background:var(--accent)}

/* ── HEADER ── */
header{
  background:rgba(8,8,26,.85);
  backdrop-filter:blur(12px);
  border-bottom:1px solid transparent;
  background-clip:padding-box;
  padding:0 1.25rem;height:50px;display:flex;align-items:center;gap:.8rem;
  flex-shrink:0;z-index:10;position:relative;
}
header::after{
  content:'';position:absolute;bottom:0;left:0;right:0;height:1px;
  background:linear-gradient(90deg,transparent 0%,rgba(139,92,246,.5) 25%,rgba(34,211,238,.5) 75%,transparent 100%);
}
.logo{font-size:1.1rem;font-weight:800;letter-spacing:-.04em}
.logo em{
  background:var(--grad);
  -webkit-background-clip:text;background-clip:text;
  -webkit-text-fill-color:transparent;
  font-style:normal;
}
.chip{background:rgba(139,92,246,.07);border:1px solid var(--b1);border-radius:99px;padding:3px 10px;font-size:.68rem;font-weight:600;color:var(--t2);letter-spacing:.04em;white-space:nowrap}
.chip.c-lsm{color:var(--accent);border-color:var(--aborder);background:var(--adim)}
.chip.c-live{color:var(--green);border-color:var(--gborder);background:var(--gdim)}
.chip strong{color:var(--t1)}
.hdr-search{margin-left:auto;position:relative}
.hdr-search svg{position:absolute;left:.6rem;top:50%;transform:translateY(-50%);color:var(--t3);pointer-events:none}
.hdr-search input{background:var(--bg2);border:1px solid var(--b1);border-radius:8px;color:var(--t1);font-size:.8rem;padding:.38rem .75rem .38rem 2rem;outline:none;width:210px;transition:border-color .15s,box-shadow .15s,width .2s}
.hdr-search input:focus{border-color:var(--accent);box-shadow:0 0 0 3px var(--adim),0 0 14px rgba(139,92,246,.12);width:270px}
.hdr-search input::placeholder{color:var(--t3)}
.pal-btn{background:var(--adim);border:1px solid var(--aborder);border-radius:7px;color:var(--t2);font-size:.71rem;padding:5px 11px;cursor:pointer;display:flex;align-items:center;gap:5px;transition:background .12s,color .12s,box-shadow .12s;white-space:nowrap}
.pal-btn:hover{background:rgba(139,92,246,.2);color:var(--t1);box-shadow:0 0 12px rgba(139,92,246,.2)}
kbd{background:rgba(139,92,246,.12);border:1px solid var(--aborder);border-radius:4px;font-size:.63rem;padding:1px 5px;font-family:var(--mono);color:var(--accent)}
.sdot{width:8px;height:8px;border-radius:50%;background:var(--green);flex-shrink:0;
  box-shadow:0 0 0 0 rgba(16,185,129,.5);
  animation:pulse 2.5s ease-in-out infinite;
}
@keyframes pulse{0%{box-shadow:0 0 0 0 rgba(16,185,129,.5)}70%{box-shadow:0 0 0 7px rgba(16,185,129,0)}100%{box-shadow:0 0 0 0 rgba(16,185,129,0)}}

/* ── LAYOUT ── */
.layout{display:flex;flex:1;overflow:hidden}

/* ── LEFT SIDEBAR ── */
.sidebar{width:195px;flex-shrink:0;background:rgba(8,8,26,.6);border-right:1px solid var(--b0);display:flex;flex-direction:column;overflow:hidden;backdrop-filter:blur(8px)}
.sb-sec{padding:.9rem 1rem .6rem}
.sb-hdr{font-size:.6rem;font-weight:700;letter-spacing:.12em;text-transform:uppercase;color:var(--t3);margin-bottom:.5rem}
.ns-item{display:flex;align-items:center;gap:.4rem;padding:.33rem .5rem;border-radius:6px;cursor:pointer;color:var(--t2);transition:background .1s,color .1s;margin-bottom:1px;user-select:none;border-left:2px solid transparent}
.ns-item:hover{background:rgba(139,92,246,.08);color:var(--t1)}
.ns-item.active{background:var(--adim);color:var(--accent);border-left-color:var(--accent)}
.ns-dot{width:5px;height:5px;border-radius:50%;background:currentColor;flex-shrink:0;opacity:.5}
.ns-item.active .ns-dot{opacity:1;box-shadow:0 0 4px var(--accent)}
.ns-name{flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-family:var(--mono);font-size:.75rem}
.ns-badge{font-size:.63rem;background:var(--bg3);padding:1px 6px;border-radius:99px;color:var(--t3);flex-shrink:0}
.ns-item.active .ns-badge{background:var(--adim);color:var(--accent)}
.ns-all{font-weight:600;color:var(--t2)}
.ns-all.active{color:var(--accent)}
.sb-div{height:1px;background:var(--b0);margin:.4rem 0}
.stat-row{display:flex;justify-content:space-between;align-items:center;padding:.28rem .5rem;font-size:.75rem;color:var(--t2)}
.stat-val{font-family:var(--mono);font-size:.79rem;color:var(--accent);font-weight:700}

/* ── CENTER ── */
.center{flex:1;display:flex;flex-direction:column;overflow:hidden;min-width:0;position:relative}
.center::before{
  content:'';position:absolute;inset:0;pointer-events:none;z-index:0;
  background-image:radial-gradient(circle at 1px 1px,rgba(139,92,246,.04) 1px,transparent 0);
  background-size:26px 26px;
}
.ctb{padding:.6rem 1rem;border-bottom:1px solid var(--b0);display:flex;align-items:center;gap:.55rem;flex-shrink:0;background:rgba(8,8,26,.7);position:relative;z-index:1;backdrop-filter:blur(8px)}
.ctb-title{font-size:.82rem;font-weight:600;white-space:nowrap}
.sel-badge{font-size:.72rem;color:var(--accent);background:var(--adim);border:1px solid var(--aborder);border-radius:99px;padding:2px 9px;display:none}
.sel-badge.show{display:block}
.bulk-del{padding:4px 11px;border-radius:6px;border:1px solid var(--rborder);background:var(--rdim);color:var(--red);font-size:.71rem;font-weight:600;cursor:pointer;display:none;transition:background .12s,box-shadow .12s}
.bulk-del.show{display:block}
.bulk-del:hover{background:rgba(248,113,113,.2);box-shadow:0 0 10px rgba(248,113,113,.15)}
.ml{margin-left:auto}
.tbtn{padding:.32rem .8rem;border-radius:6px;border:1px solid var(--b1);background:transparent;color:var(--t2);font-size:.71rem;font-weight:600;cursor:pointer;transition:background .1s,color .1s,border-color .1s}
.tbtn:hover{background:var(--adim);color:var(--accent);border-color:var(--aborder)}

/* ── TABLE ── */
.tscroll{flex:1;overflow-y:auto;position:relative;z-index:1}
table{width:100%;border-collapse:collapse}
th{position:sticky;top:0;z-index:1;background:rgba(8,8,26,.85);backdrop-filter:blur(8px);border-bottom:1px solid var(--b1);padding:.5rem .9rem;font-size:.62rem;font-weight:700;text-transform:uppercase;letter-spacing:.1em;color:var(--t3);text-align:left;white-space:nowrap;user-select:none}
th.sort{cursor:pointer}
th.sort:hover{color:var(--t2)}
th.sorted{color:var(--accent)}
.sarr{margin-left:3px;opacity:.8}
th:first-child,td:first-child{width:32px;padding-right:0}
.cb{width:13px;height:13px;accent-color:var(--accent);cursor:pointer}
td{padding:.5rem .9rem;border-bottom:1px solid var(--b0);font-family:var(--mono);font-size:.78rem;vertical-align:middle}
tr:last-child td{border-bottom:none}
tr:hover td{background:rgba(139,92,246,.05)}
tr.sel td{background:rgba(139,92,246,.1) !important}
tr.row-in td{animation:rIn .35s ease}
tr.row-out td{animation:rOut .28s ease forwards;pointer-events:none}
@keyframes rIn{from{opacity:0;background:rgba(16,185,129,.1)}to{opacity:1}}
@keyframes rOut{to{opacity:0;background:rgba(248,113,113,.1)}}
.kc{color:var(--accent);font-weight:500;cursor:pointer;max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.kc:hover{color:var(--cyan);text-decoration:underline;text-decoration-color:var(--cyan)}
.vc{color:var(--t1);max-width:280px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.iinp{background:var(--bg2);border:1px solid var(--accent);border-radius:5px;color:var(--t1);font-family:var(--mono);font-size:.78rem;padding:2px 6px;outline:none;width:100%;box-shadow:0 0 0 3px var(--adim),0 0 12px rgba(139,92,246,.15)}
.ac{width:84px}
.abtns{display:flex;gap:.25rem;opacity:0;transition:opacity .12s}
tr:hover .abtns,tr.sel .abtns{opacity:1}
.ib{width:25px;height:25px;border-radius:5px;border:1px solid var(--b1);background:transparent;color:var(--t3);cursor:pointer;display:flex;align-items:center;justify-content:center;transition:all .1s;padding:0;flex-shrink:0}
.ib.c:hover{background:var(--bdim);color:var(--blue);border-color:var(--bborder);box-shadow:0 0 8px rgba(56,189,248,.15)}
.ib.e:hover{background:var(--adim);color:var(--accent);border-color:var(--aborder);box-shadow:0 0 8px rgba(139,92,246,.2)}
.ib.d:hover{background:var(--rdim);color:var(--red);border-color:var(--rborder);box-shadow:0 0 8px rgba(248,113,113,.15)}
.empty{padding:5rem 2rem;text-align:center;color:var(--t3)}
.empty p{font-size:.9rem;margin-bottom:.4rem;color:var(--t2)}
.empty small{font-size:.74rem}

/* ── RIGHT PANEL ── */
.rpanel{width:296px;flex-shrink:0;background:rgba(8,8,26,.7);border-left:1px solid var(--b0);display:flex;flex-direction:column;overflow:hidden;backdrop-filter:blur(8px)}
.rp-tabs{display:flex;border-bottom:1px solid var(--b0);position:relative;flex-shrink:0}
.rp-tab{flex:1;padding:.62rem 0;text-align:center;font-size:.68rem;font-weight:700;letter-spacing:.07em;cursor:pointer;color:var(--t3);transition:color .12s;user-select:none}
.rp-tab.active{color:var(--t1)}
.rp-tbar{
  position:absolute;bottom:0;height:2px;
  background:var(--grad);
  transition:left .18s ease,width .18s ease;border-radius:2px 2px 0 0;
  box-shadow:0 0 8px rgba(139,92,246,.6);
}
.rp-pane{display:none;flex-direction:column;gap:.58rem;padding:.95rem;flex:1;overflow-y:auto}
.rp-pane.active{display:flex}
.flbl{font-size:.62rem;font-weight:700;letter-spacing:.08em;text-transform:uppercase;color:var(--t3);margin-bottom:.18rem}
.finp{width:100%;background:rgba(5,5,15,.7);border:1px solid var(--b1);border-radius:6px;padding:.48rem .65rem;color:var(--t1);font-family:var(--mono);font-size:.78rem;outline:none;transition:border-color .12s,box-shadow .12s}
.finp:focus{border-color:var(--accent);box-shadow:0 0 0 3px var(--adim),0 0 14px rgba(139,92,246,.12)}
.finp::placeholder{color:var(--t3)}
.fg{display:flex;flex-direction:column}
.fbtn{width:100%;padding:.53rem;border-radius:6px;border:none;font-size:.77rem;font-weight:700;letter-spacing:.03em;cursor:pointer;transition:opacity .12s,transform .1s,box-shadow .12s;margin-top:.08rem}
.fbtn:hover{opacity:.92;box-shadow:0 4px 16px rgba(0,0,0,.4)}.fbtn:active{transform:scale(.98)}.fbtn:disabled{opacity:.3;cursor:not-allowed}
.fp{background:linear-gradient(135deg,#059669,#0891b2);color:#fff}
.fg2{background:linear-gradient(135deg,#2563eb,#0284c7);color:#fff}
.fd{background:linear-gradient(135deg,#dc2626,#be185d);color:#fff}
.fs{background:linear-gradient(135deg,#7c3aed,#4f46e5);color:#fff}
.rbox{border:1px solid var(--b1);border-radius:6px;background:rgba(5,5,15,.5);padding:.52rem .65rem;font-size:.75rem;font-family:var(--mono);line-height:1.55;white-space:pre-wrap;word-break:break-all;color:var(--t3);min-height:2.1rem}
.rbox.ok{color:var(--green);border-color:var(--gborder);background:rgba(16,185,129,.04)}
.rbox.err{color:var(--red);border-color:var(--rborder);background:rgba(248,113,113,.04)}
.rbox.info{color:var(--cyan);border-color:var(--cborder);background:rgba(34,211,238,.04)}
.sr{display:flex;gap:.4rem;align-items:baseline;padding:.25rem 0;border-bottom:1px solid var(--b0);font-size:.74rem;font-family:var(--mono)}
.sr:last-child{border-bottom:none}
.sr-k{color:var(--accent);flex-shrink:0}
.sr-a{color:var(--t3);flex-shrink:0}
.sr-v{color:var(--t1);word-break:break-all}

/* ── LOG ── */
.rp-log{border-top:1px solid var(--b0);padding:.7rem .95rem;flex-shrink:0;display:flex;flex-direction:column;max-height:175px}
.rp-log-h{font-size:.6rem;font-weight:700;letter-spacing:.12em;text-transform:uppercase;color:var(--t3);margin-bottom:.38rem;flex-shrink:0}
.rp-log-l{overflow-y:auto;display:flex;flex-direction:column;gap:.14rem}
.le{display:flex;gap:.5rem;align-items:baseline;font-size:.69rem;font-family:var(--mono);animation:leIn .2s ease}
@keyframes leIn{from{opacity:0;transform:translateY(-3px)}to{opacity:1}}
.le-t{color:var(--t3);flex-shrink:0}
.le-o{font-weight:700;flex-shrink:0}
.le-o.PUT{color:var(--green)}.le-o.GET{color:var(--cyan)}.le-o.DEL{color:var(--red)}.le-o.SCAN{color:var(--purple)}
.le-k{color:var(--t2);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1;min-width:0}
.le-s{flex-shrink:0}
.le-s.ok{color:var(--green)}.le-s.err{color:var(--red)}

/* ── STATUS BAR ── */
.sbar{background:rgba(5,5,15,.85);backdrop-filter:blur(8px);border-top:1px solid var(--b0);height:26px;display:flex;align-items:center;padding:0 1rem;gap:1.1rem;flex-shrink:0;font-size:.69rem;color:var(--t3)}
.sbar-i{display:flex;align-items:center;gap:.35rem}
.sbar-dot{width:6px;height:6px;border-radius:50%;background:var(--green);box-shadow:0 0 5px var(--green)}
.ob{font-family:var(--mono);font-size:.66rem;padding:0 5px;border-radius:3px;font-weight:700}
.ob.PUT{background:var(--gdim);color:var(--green)}.ob.GET{background:var(--bdim);color:var(--cyan)}
.ob.DEL{background:var(--rdim);color:var(--red)}.ob.SCAN{background:rgba(192,132,252,.12);color:var(--purple)}
.sbar-key{font-family:var(--mono);font-size:.67rem;color:var(--t2)}
.ns-label{color:var(--accent)}

/* ── COMMAND PALETTE ── */
.pal-ov{position:fixed;inset:0;background:rgba(2,2,12,.75);backdrop-filter:blur(8px);z-index:100;display:none;align-items:flex-start;justify-content:center;padding-top:13vh}
.pal-ov.show{display:flex}
.pal{
  background:rgba(11,11,30,.95);
  border:1px solid rgba(139,92,246,.3);
  border-radius:14px;width:540px;
  box-shadow:var(--shadow),0 0 0 1px rgba(255,255,255,.03),0 0 60px rgba(139,92,246,.12);
  overflow:hidden;animation:palIn .17s ease;
}
@keyframes palIn{from{opacity:0;transform:scale(.95) translateY(-10px)}to{opacity:1;transform:none}}
.pal-iw{position:relative}
.pal-icon{position:absolute;left:1rem;top:50%;transform:translateY(-50%);color:var(--accent)}
.pal-inp{width:100%;background:transparent;border:none;border-bottom:1px solid rgba(139,92,246,.18);color:var(--t1);font-size:.93rem;font-family:var(--mono);padding:.95rem 1rem .95rem 2.75rem;outline:none}
.pal-inp::placeholder{color:var(--t3);font-family:system-ui;font-size:.88rem}
.pal-body{padding:.45rem}
.pal-sh{font-size:.6rem;font-weight:700;letter-spacing:.12em;text-transform:uppercase;color:var(--t3);padding:.38rem .5rem .18rem}
.pal-item{display:flex;align-items:center;gap:.55rem;padding:.48rem .55rem;border-radius:7px;cursor:pointer;transition:background .1s}
.pal-item:hover,.pal-item.focus{background:rgba(139,92,246,.1)}
.pal-item.focus{border-left:2px solid var(--accent);padding-left:calc(.55rem - 2px)}
.pal-ico{width:27px;height:27px;border-radius:6px;display:flex;align-items:center;justify-content:center;font-size:.68rem;font-weight:700;flex-shrink:0}
.ico-p{background:var(--gdim);color:var(--green)}.ico-g{background:var(--bdim);color:var(--cyan)}
.ico-d{background:var(--rdim);color:var(--red)}.ico-s{background:rgba(192,132,252,.14);color:var(--purple)}
.ico-k{background:var(--adim);color:var(--accent);font-size:.6rem;overflow:hidden}
.pal-main{flex:1;min-width:0}
.pal-title{color:var(--t1);font-family:var(--mono);font-size:.79rem}
.pal-desc{color:var(--t3);font-size:.71rem;margin-top:1px}
.pal-foot{padding:.45rem 1rem;border-top:1px solid rgba(139,92,246,.1);display:flex;gap:1rem;font-size:.68rem;color:var(--t3)}
.pal-hint{display:flex;align-items:center;gap:.3rem}

/* ── TOASTS ── */
#toasts{position:fixed;top:58px;right:1rem;display:flex;flex-direction:column;gap:.38rem;z-index:200;pointer-events:none}
.toast{
  background:rgba(11,11,30,.95);
  border:1px solid rgba(139,92,246,.25);
  border-radius:10px;padding:.58rem .9rem;display:flex;align-items:center;gap:.5rem;
  font-size:.78rem;
  box-shadow:var(--shadow),0 0 20px rgba(139,92,246,.08);
  animation:tIn .2s ease;pointer-events:auto;max-width:275px;min-width:155px;
}
@keyframes tIn{from{opacity:0;transform:translateX(14px)}to{opacity:1}}
.toast.to{animation:tOut .2s ease forwards}
@keyframes tOut{to{opacity:0;transform:translateX(14px)}}
.tdot{width:7px;height:7px;border-radius:50%;flex-shrink:0}
.toast.ok .tdot{background:var(--green);box-shadow:0 0 6px var(--green)}
.toast.err .tdot{background:var(--red);box-shadow:0 0 6px var(--red)}
.toast.inf .tdot{background:var(--cyan);box-shadow:0 0 6px var(--cyan)}
</style>
</head>
<body>

<header>
  <div class="logo">go<em>store</em></div>
  <div class="chip c-lsm">LSM-tree</div>
  <div class="chip c-live"><strong id="hdr-n">0</strong> keys</div>
  <div class="chip"><strong id="hdr-ns">0</strong> namespaces</div>
  <div class="hdr-search">
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
    <input id="hdr-q" placeholder="Search keys and values..." oninput="onSearch()">
  </div>
  <button class="pal-btn" onclick="openPal()">
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/></svg>
    Command palette <kbd>Ctrl K</kbd>
  </button>
  <div class="sdot" title="Connected"></div>
</header>

<!-- PALETTE -->
<div class="pal-ov" id="pal-ov" onclick="onPalOvClick(event)">
  <div class="pal">
    <div class="pal-iw">
      <svg class="pal-icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/></svg>
      <input class="pal-inp" id="pal-inp" placeholder="put key value  |  get key  |  del key  |  scan start end" oninput="onPalIn()" onkeydown="onPalKey(event)">
    </div>
    <div class="pal-body" id="pal-body"></div>
    <div class="pal-foot">
      <div class="pal-hint"><kbd>Enter</kbd> execute</div>
      <div class="pal-hint"><kbd>&#8593;&#8595;</kbd> navigate</div>
      <div class="pal-hint"><kbd>Esc</kbd> close</div>
    </div>
  </div>
</div>

<div id="toasts"></div>

<div class="layout">

  <!-- LEFT: namespace tree -->
  <div class="sidebar">
    <div class="sb-sec">
      <div class="sb-hdr">Namespaces</div>
      <div id="ns-tree"></div>
    </div>
    <div class="sb-div"></div>
    <div class="sb-sec">
      <div class="sb-hdr">Session</div>
      <div class="stat-row"><span>Keys</span><span class="stat-val" id="st-keys">0</span></div>
      <div class="stat-row"><span>Namespaces</span><span class="stat-val" id="st-ns">0</span></div>
      <div class="stat-row"><span>Operations</span><span class="stat-val" id="st-ops">0</span></div>
    </div>
  </div>

  <!-- CENTER: explorer -->
  <div class="center">
    <div class="ctb">
      <span class="ctb-title" id="ctb-title">All Keys</span>
      <span class="sel-badge" id="sel-badge"></span>
      <button class="bulk-del" id="bulk-del-btn" onclick="bulkDel()">Delete selected</button>
      <div class="ml"></div>
      <button class="tbtn" onclick="loadAll()">&#8635; Refresh</button>
    </div>
    <div class="tscroll">
      <table>
        <thead>
          <tr>
            <th><input type="checkbox" class="cb" id="cb-all" onclick="toggleAllSel()"></th>
            <th class="sort sorted" id="th-k" onclick="sortBy('key')">Key <span class="sarr" id="sa-k">&#8593;</span></th>
            <th class="sort" id="th-v" onclick="sortBy('value')">Value <span class="sarr" id="sa-v" style="display:none">&#8593;</span></th>
            <th></th>
          </tr>
        </thead>
        <tbody id="tbody"></tbody>
      </table>
    </div>
  </div>

  <!-- RIGHT: operations -->
  <div class="rpanel">
    <div class="rp-tabs">
      <div class="rp-tab active" onclick="switchTab(0)">PUT</div>
      <div class="rp-tab" onclick="switchTab(1)">GET</div>
      <div class="rp-tab" onclick="switchTab(2)">DEL</div>
      <div class="rp-tab" onclick="switchTab(3)">SCAN</div>
      <div class="rp-tbar" id="rp-tbar"></div>
    </div>

    <div class="rp-pane active" id="rp-p0">
      <div class="fg"><div class="flbl">Key</div><input class="finp" id="put-k" placeholder="e.g. user:alice"></div>
      <div class="fg"><div class="flbl">Value</div><input class="finp" id="put-v" placeholder="e.g. Alice Smith"></div>
      <button class="fbtn fp" id="btn-put" onclick="doPut()">Put</button>
      <div class="rbox" id="r-put">—</div>
    </div>

    <div class="rp-pane" id="rp-p1">
      <div class="fg"><div class="flbl">Key</div><input class="finp" id="get-k" placeholder="e.g. user:alice"></div>
      <button class="fbtn fg2" id="btn-get" onclick="doGet()">Get</button>
      <div class="rbox" id="r-get">—</div>
    </div>

    <div class="rp-pane" id="rp-p2">
      <div class="fg"><div class="flbl">Key</div><input class="finp" id="del-k" placeholder="e.g. user:alice"></div>
      <button class="fbtn fd" id="btn-del" onclick="doDel()">Delete</button>
      <div class="rbox" id="r-del">—</div>
    </div>

    <div class="rp-pane" id="rp-p3">
      <div class="fg">
        <div class="flbl">Start <small style="font-weight:400;text-transform:none;letter-spacing:0">(inclusive)</small></div>
        <input class="finp" id="scan-s" placeholder="blank = no lower bound">
      </div>
      <div class="fg">
        <div class="flbl">End <small style="font-weight:400;text-transform:none;letter-spacing:0">(exclusive)</small></div>
        <input class="finp" id="scan-e" placeholder="blank = no upper bound">
      </div>
      <button class="fbtn fs" id="btn-scan" onclick="doScan()">Scan</button>
      <div class="rbox info" id="r-scan">—</div>
    </div>

    <div class="rp-log">
      <div class="rp-log-h">Recent Activity</div>
      <div class="rp-log-l" id="log-l"></div>
    </div>
  </div>
</div>

<div class="sbar">
  <div class="sbar-i"><div class="sbar-dot"></div><span>Connected</span></div>
  <div class="sbar-i" id="sb-last" style="display:none">
    Last: <span class="ob" id="sb-op"></span>
    <span class="sbar-key" id="sb-key"></span>
    &mdash; <span id="sb-ms"></span> &mdash; <span id="sb-ago"></span>
  </div>
  <div class="ml"></div>
  <span class="ns-label" id="sb-ns" style="display:none"></span>
  <span style="color:var(--t3)">gostore v1.0</span>
</div>

<script>
var rows=[], logs=[], ops=0;
var tab=0, nsF=null, q='', sCol='key', sDir=1;
var sel=new Set();
var palItems=[], palFoc=-1, palOpen=false;
var lastOpTs=null, editing=false;

/* ── API ── */
async function api(m,p,b){
  var t=Date.now(), r=await fetch(p,{method:m,headers:b!==undefined?{'Content-Type':'text/plain'}:{},body:b});
  return {ok:r.ok,st:r.status,data:await r.json(),ms:Date.now()-t};
}

/* ── LOAD ── */
async function loadAll(hl){
  var r=await api('GET','/api/keys');
  if(!r.ok)return;
  rows=r.data||[];
  buildTree(); renderTable(hl); updateStats();
  g('hdr-n').textContent=rows.length;
}

function getNs(k){var i=k.indexOf(':');if(i<0)i=k.indexOf('/');return i<0?'(root)':k.slice(0,i)}

function buildTree(){
  var ns={};
  rows.forEach(function(r){var p=getNs(r.key);ns[p]=(ns[p]||0)+1;});
  var nsKeys=Object.keys(ns).sort();
  g('hdr-ns').textContent=nsKeys.length;
  g('st-ns').textContent=nsKeys.length;
  var html='<div class="ns-item ns-all'+(nsF===null?' active':'')+'" data-ns="">'
    +'<div class="ns-dot"></div><div class="ns-name">All keys</div>'
    +'<div class="ns-badge">'+rows.length+'</div></div>';
  nsKeys.forEach(function(n){
    html+='<div class="ns-item'+(nsF===n?' active':'')+'" data-ns="'+ea(n)+'">'
      +'<div class="ns-dot"></div><div class="ns-name">'+e(n)+'</div>'
      +'<div class="ns-badge">'+ns[n]+'</div></div>';
  });
  g('ns-tree').innerHTML=html;
}

g('ns-tree') && document.getElementById('ns-tree').addEventListener('click',function(ev){
  var item=ev.target.closest('.ns-item');
  if(!item)return;
  var ns=item.dataset.ns;
  nsF=ns===''?null:ns;
  sel.clear(); buildTree(); renderTable();
  var sb=g('sb-ns'); var ctb=g('ctb-title');
  if(nsF){sb.textContent='ns: '+nsF;sb.style.display='';ctb.textContent=nsF+':* keys';}
  else{sb.style.display='none';ctb.textContent='All Keys';}
});

function onSearch(){q=g('hdr-q').value.toLowerCase();renderTable();}

function renderTable(hl){
  var vis=rows.filter(function(r){
    return (nsF===null||getNs(r.key)===nsF)&&(!q||r.key.toLowerCase().includes(q)||r.value.toLowerCase().includes(q));
  });
  vis.sort(function(a,b){var av=sCol==='key'?a.key:a.value,bv=sCol==='key'?b.key:b.value;return av<bv?-sDir:av>bv?sDir:0;});
  var tb=g('tbody');
  if(!vis.length){
    tb.innerHTML='<tr><td colspan="4"><div class="empty"><p>'+(q||nsF?'No matching keys':'No keys yet')+'</p>'
      +'<small>'+(q?'Try a different search':nsF?'This namespace is empty':'Use Put to add one')+'</small></div></td></tr>';
    updSelUI(); return;
  }
  tb.innerHTML=vis.map(function(r){
    var s=sel.has(r.key);
    return '<tr class="'+(r.key===hl?'row-in':'')+(s?' sel':'')+'" data-key="'+ea(r.key)+'" data-val="'+ea(r.value)+'">'
      +'<td><input type="checkbox" class="cb"'+(s?' checked':'')+' onclick="onCB(event)"></td>'
      +'<td class="kc" title="'+ea(r.key)+'">'+e(r.key)+'</td>'
      +'<td class="vc" title="Double-click to edit">'+e(r.value)+'</td>'
      +'<td class="ac"><div class="abtns">'
      +'<button class="ib c" title="Copy value"><svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/></svg></button>'
      +'<button class="ib e" title="Edit in panel"><svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7"/><path d="M18.5 2.5a2.12 2.12 0 013 3L12 15l-4 1 1-4z"/></svg></button>'
      +'<button class="ib d" title="Delete"><svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14H6L5 6"/><line x1="10" y1="11" x2="10" y2="17"/><line x1="14" y1="11" x2="14" y2="17"/><path d="M9 6V4h6v2"/></svg></button>'
      +'</div></td></tr>';
  }).join('');
  updSelUI();
}

/* event delegation — table clicks */
document.getElementById('tbody').addEventListener('click',function(ev){
  var row=ev.target.closest('tr');
  if(!row)return;
  var key=row.dataset.key, val=row.dataset.val;
  if(ev.target.closest('.c')){cpv(val);return;}
  if(ev.target.closest('.e')){editPanel(key,val);return;}
  if(ev.target.closest('.d')){qDel(key);return;}
  if(ev.target.closest('.kc')){cpv(key);return;}
});
document.getElementById('tbody').addEventListener('dblclick',function(ev){
  var td=ev.target.closest('td.vc');
  if(!td)return;
  var row=td.closest('tr');
  startInlineEdit(td,row.dataset.key,row.dataset.val);
});

/* ── SORT ── */
function sortBy(c){
  if(sCol===c)sDir*=-1;else{sCol=c;sDir=1;}
  g('th-k').className='sort'+(sCol==='key'?' sorted':'');
  g('th-v').className='sort'+(sCol==='value'?' sorted':'');
  g('sa-k').style.display=sCol==='key'?'':'none';
  g('sa-v').style.display=sCol==='value'?'':'none';
  g('sa-k').innerHTML=sDir===1?'&#8593;':'&#8595;';
  g('sa-v').innerHTML=sDir===1?'&#8593;':'&#8595;';
  renderTable();
}

/* ── SELECTION ── */
function onCB(ev){
  var row=ev.target.closest('tr'),key=row.dataset.key;
  if(ev.target.checked)sel.add(key);else sel.delete(key);
  row.classList.toggle('sel',ev.target.checked);
  updSelUI();
}
function toggleAllSel(){
  var on=g('cb-all').checked;
  document.querySelectorAll('#tbody tr[data-key]').forEach(function(row){
    var cb=row.querySelector('.cb');
    cb.checked=on;
    if(on)sel.add(row.dataset.key);else sel.delete(row.dataset.key);
    row.classList.toggle('sel',on);
  });
  updSelUI();
}
function updSelUI(){
  var n=sel.size;
  var sb=g('sel-badge'),db=g('bulk-del-btn');
  if(n){sb.textContent=n+' selected';sb.classList.add('show');db.classList.add('show');}
  else{sb.classList.remove('show');db.classList.remove('show');}
}
async function bulkDel(){
  var keys=Array.from(sel);
  for(var i=0;i<keys.length;i++){
    var r=await api('DELETE','/api/keys/'+encodeURIComponent(keys[i]));
    if(r.ok){addLog('DEL',keys[i],true,r.ms);markDel(keys[i]);}
  }
  sel.clear(); toast('Deleted '+keys.length+' key'+(keys.length>1?'s':''),'ok');
  setTimeout(loadAll,320); incOps();
}

/* ── INLINE EDIT ── */
function startInlineEdit(td,key,cur){
  if(editing)return; editing=true;
  var saved=td.innerHTML;
  var inp=document.createElement('input');
  inp.className='iinp'; inp.value=cur;
  td.innerHTML=''; td.appendChild(inp);
  inp.focus(); inp.select();
  var done=false;
  inp.addEventListener('keydown',function(ev){
    if(ev.key==='Enter'){ev.preventDefault();done=true;commitEdit(key,inp.value,td,saved);}
    if(ev.key==='Escape'){ev.preventDefault();cancelEdit(td,saved);}
  });
  inp.addEventListener('blur',function(){setTimeout(function(){if(!done)cancelEdit(td,saved);},70);});
}
async function commitEdit(key,val,td,saved){
  editing=false;
  var r=await api('PUT','/api/keys/'+encodeURIComponent(key),val);
  incOps();
  if(r.ok){toast('Updated '+key,'ok');addLog('PUT',key,true,r.ms);updSB('PUT',key,r.ms);await loadAll();}
  else{toast(r.data.error,'err');td.innerHTML=saved;}
}
function cancelEdit(td,saved){editing=false;td.innerHTML=saved;}

/* ── OPERATIONS ── */
async function doPut(){
  var k=gv('put-k'),v2=gv('put-v'),res=g('r-put');
  if(!k){setR(res,'key is required','err');return;}
  ld('btn-put',true);
  var r=await api('PUT','/api/keys/'+encodeURIComponent(k),v2);
  ld('btn-put',false); incOps();
  if(r.ok){setR(res,'stored  "'+k+'"','ok');toast('Stored  '+k,'ok');addLog('PUT',k,true,r.ms);updSB('PUT',k,r.ms);await loadAll(k);}
  else{setR(res,r.data.error,'err');toast(r.data.error,'err');addLog('PUT',k,false,r.ms);}
}
async function doGet(){
  var k=gv('get-k'),res=g('r-get');
  if(!k){setR(res,'key is required','err');return;}
  ld('btn-get',true);
  var r=await api('GET','/api/keys/'+encodeURIComponent(k));
  ld('btn-get',false); incOps();
  if(r.ok){setR(res,r.data.value,'ok');addLog('GET',k,true,r.ms);updSB('GET',k,r.ms);}
  else{setR(res,r.st===404?'key not found':'error: '+r.data.error,r.st===404?'info':'err');addLog('GET',k,false,r.ms);updSB('GET',k,r.ms);}
}
async function doDel(){
  var k=gv('del-k'),res=g('r-del');
  if(!k){setR(res,'key is required','err');return;}
  ld('btn-del',true);
  var r=await api('DELETE','/api/keys/'+encodeURIComponent(k));
  ld('btn-del',false); incOps();
  if(r.ok){setR(res,'deleted  "'+k+'"','ok');toast('Deleted  '+k,'ok');addLog('DEL',k,true,r.ms);updSB('DEL',k,r.ms);markDel(k);setTimeout(loadAll,320);}
  else{setR(res,r.data.error,'err');toast(r.data.error,'err');addLog('DEL',k,false,r.ms);}
}
async function doScan(){
  var s=gv('scan-s'),ee=gv('scan-e'),res=g('r-scan');
  var url='/api/scan?';
  if(s)url+='start='+encodeURIComponent(s)+'&';
  if(ee)url+='end='+encodeURIComponent(ee);
  ld('btn-scan',true);
  var r=await api('GET',url);
  ld('btn-scan',false); incOps();
  var lk=(s||'*')+' to '+(ee||'*');
  addLog('SCAN',lk,r.ok,r.ms); updSB('SCAN',lk,r.ms);
  if(!r.ok){setR(res,r.data.error,'err');return;}
  if(!r.data.length){setR(res,'(no keys in range)','info');return;}
  res.className='rbox';
  res.innerHTML=r.data.map(function(row){
    return '<div class="sr"><span class="sr-k">'+e(row.key)+'</span><span class="sr-a">&#8594;</span><span class="sr-v">'+e(row.value)+'</span></div>';
  }).join('');
}
async function qDel(key){
  var r=await api('DELETE','/api/keys/'+encodeURIComponent(key));
  incOps();
  if(r.ok){toast('Deleted  '+key,'ok');addLog('DEL',key,true,r.ms);updSB('DEL',key,r.ms);markDel(key);setTimeout(loadAll,320);}
  else toast(r.data.error,'err');
}
function editPanel(key,val){g('put-k').value=key;g('put-v').value=val;switchTab(0);setTimeout(function(){g('put-v').focus();g('put-v').select();},50);}
async function cpv(v2){try{await navigator.clipboard.writeText(v2);toast('Copied','inf');}catch(ex){toast('Copy failed','err');}}

/* ── TABS ── */
function switchTab(i){
  document.querySelectorAll('.rp-tab').forEach(function(t,j){t.classList.toggle('active',i===j)});
  document.querySelectorAll('.rp-pane').forEach(function(p,j){p.classList.toggle('active',i===j)});
  tab=i; placeBar(i);
  var inp=g('rp-p'+i).querySelector('.finp');
  if(inp)setTimeout(function(){inp.focus();},45);
}
function placeBar(i){
  var tabs=document.querySelectorAll('.rp-tab'),t=tabs[i],bar=g('rp-tbar');
  if(!t||!bar)return;
  bar.style.left=t.offsetLeft+'px'; bar.style.width=t.offsetWidth+'px';
}

/* ── COMMAND PALETTE ── */
var CMDS=[
  {ico:'ico-p',lbl:'PUT',op:'put',desc:'Write a key-value pair',ex:'put user:alice Alice Smith'},
  {ico:'ico-g',lbl:'GET',op:'get',desc:'Read a key',ex:'get user:alice'},
  {ico:'ico-d',lbl:'DEL',op:'del',desc:'Delete a key',ex:'del user:alice'},
  {ico:'ico-s',lbl:'SCAN',op:'scan',desc:'Range scan between two keys',ex:'scan user: user;'},
];
function openPal(){
  palOpen=true; palFoc=-1;
  g('pal-ov').classList.add('show');
  g('pal-inp').value='';
  renderPal('');
  setTimeout(function(){g('pal-inp').focus();},70);
}
function closePal(){palOpen=false;g('pal-ov').classList.remove('show');}
function onPalOvClick(ev){if(ev.target===g('pal-ov'))closePal();}
function onPalIn(){palFoc=-1;renderPal(g('pal-inp').value);}
function renderPal(inp){
  var body=g('pal-body'), qt=inp.trim().toLowerCase(), html='';
  palItems=[];
  if(!qt){
    html+='<div class="pal-sh">Operations</div>';
    CMDS.forEach(function(c,i){
      palItems.push({type:'fill',text:c.op+' '});
      html+='<div class="pal-item" data-pi="'+i+'">'
        +'<div class="pal-ico '+c.ico+'">'+c.lbl+'</div>'
        +'<div class="pal-main"><div class="pal-title">'+c.lbl+'</div>'
        +'<div class="pal-desc">'+c.desc+' &mdash; e.g. <code style="font-family:var(--mono);font-size:.68rem;color:var(--t2)">'+c.ex+'</code></div></div></div>';
    });
  } else {
    var parts=qt.split(/\s+/), op=parts[0], rest=parts.slice(1).join(' ');
    var mc=CMDS.find(function(c){return c.op.startsWith(op)||op.startsWith(c.op);});
    if(mc){
      html+='<div class="pal-sh">Execute</div>';
      palItems.push({type:'exec',q:inp.trim()});
      html+='<div class="pal-item focus" data-pi="0">'
        +'<div class="pal-ico '+mc.ico+'">'+mc.lbl+'</div>'
        +'<div class="pal-main"><div class="pal-title" style="font-family:var(--mono)">'+e(inp.trim())+'</div>'
        +'<div class="pal-desc">Press Enter to execute</div></div></div>';
      if((op==='get'||op==='del')&&rest){
        var sugg=rows.filter(function(r){return r.key.toLowerCase().startsWith(rest);}).slice(0,5);
        if(sugg.length){
          html+='<div class="pal-sh">Matching keys</div>';
          sugg.forEach(function(m,i){
            var fq=op+' '+m.key;
            palItems.push({type:'exec',q:fq});
            html+='<div class="pal-item" data-pi="'+(i+1)+'">'
              +'<div class="pal-ico ico-k">key</div>'
              +'<div class="pal-main"><div class="pal-title" style="font-family:var(--mono)">'+e(m.key)+'</div>'
              +'<div class="pal-desc" style="font-family:var(--mono)">'+e(m.value)+'</div></div></div>';
          });
        }
      }
      if(palFoc<0)palFoc=0;
    } else {
      html='<div style="padding:.6rem 1rem 1rem;color:var(--t3);font-size:.78rem">Try: <code style="font-family:var(--mono)">put key value</code>, <code style="font-family:var(--mono)">get key</code>, <code style="font-family:var(--mono)">del key</code>, <code style="font-family:var(--mono)">scan s e</code></div>';
    }
  }
  body.innerHTML=html;
  hilPal();
}
function hilPal(){document.querySelectorAll('.pal-item').forEach(function(el,i){el.classList.toggle('focus',i===palFoc);});}
function onPalKey(ev){
  if(ev.key==='Escape'){ev.preventDefault();closePal();return;}
  if(ev.key==='ArrowDown'){ev.preventDefault();palFoc=Math.min(palFoc+1,palItems.length-1);hilPal();return;}
  if(ev.key==='ArrowUp'){ev.preventDefault();palFoc=Math.max(palFoc-1,0);hilPal();return;}
  if(ev.key==='Enter'){ev.preventDefault();var item=palFoc>=0?palItems[palFoc]:palItems[0];if(item)runPalItem(item);return;}
}
g('pal-body').addEventListener('click',function(ev){
  var item=ev.target.closest('.pal-item');
  if(!item)return;
  var i=parseInt(item.dataset.pi);
  if(!isNaN(i)&&palItems[i])runPalItem(palItems[i]);
});
function runPalItem(item){
  if(item.type==='fill'){g('pal-inp').value=item.text;g('pal-inp').focus();renderPal(item.text);return;}
  if(item.type==='exec'){
    var parts=item.q.trim().split(/\s+/), op=parts[0].toLowerCase();
    closePal();
    if(op==='put'){g('put-k').value=parts[1]||'';g('put-v').value=parts.slice(2).join(' ');switchTab(0);doPut();}
    else if(op==='get'){g('get-k').value=parts[1]||'';switchTab(1);doGet();}
    else if(op==='del'){g('del-k').value=parts[1]||'';switchTab(2);doDel();}
    else if(op==='scan'){g('scan-s').value=parts[1]||'';g('scan-e').value=parts[2]||'';switchTab(3);doScan();}
  }
}

/* ── LOG ── */
function addLog(op,key,ok,ms){
  var now=new Date(), t=pad(now.getHours())+':'+pad(now.getMinutes())+':'+pad(now.getSeconds());
  logs.unshift({op:op,key:key,ok:ok,ms:ms,t:t});
  if(logs.length>30)logs.pop();
  g('log-l').innerHTML=logs.map(function(le){
    return '<div class="le"><span class="le-t">'+le.t+'</span>'
      +'<span class="le-o '+le.op+'">'+le.op+'</span>'
      +'<span class="le-k">'+e(le.key)+'</span>'
      +'<span class="le-s '+(le.ok?'ok':'err')+'">'+(le.ok?'&#10003;':'&#10007;')+'</span></div>';
  }).join('');
}

/* ── STATUS BAR ── */
function updSB(op,key,ms){
  lastOpTs=Date.now();
  g('sb-op').textContent=op; g('sb-op').className='ob '+op;
  g('sb-key').textContent=key.length>22?key.slice(0,20)+'&#8230;':key;
  g('sb-ms').textContent=ms+'ms';
  g('sb-last').style.display='flex';
  updAgo();
}
function updAgo(){if(!lastOpTs)return;var d=Math.floor((Date.now()-lastOpTs)/1000);g('sb-ago').textContent=d<60?d+'s ago':Math.floor(d/60)+'m ago';}
setInterval(updAgo,1000);

/* ── HELPERS ── */
function g(id){return document.getElementById(id)}
function gv(id){return g(id).value.trim()}
function pad(n){return n<10?'0'+n:''+n}
function incOps(){ops++;g('st-ops').textContent=ops;}
function updateStats(){g('st-keys').textContent=rows.length;}
function markDel(key){document.querySelectorAll('#tbody tr[data-key]').forEach(function(row){if(row.dataset.key===key)row.classList.add('row-out');});}
function setR(el,msg,cls){el.textContent=msg;el.className='rbox '+(cls||'');}
function ld(id,on){var b=g(id);if(!b)return;b.disabled=on;if(on){b.dataset.o=b.textContent;b.textContent='...';}else{b.textContent=b.dataset.o||b.textContent;}}
function e(s){return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}
function ea(s){return e(s).replace(/'/g,'&#39;');}
function toast(msg,type){
  var el=document.createElement('div');
  el.className='toast '+type;
  el.innerHTML='<div class="tdot"></div><span>'+e(msg)+'</span>';
  g('toasts').appendChild(el);
  setTimeout(function(){el.classList.add('to');setTimeout(function(){el.remove();},220);},3000);
}

/* ── KEYBOARD ── */
document.addEventListener('keydown',function(ev){
  if((ev.ctrlKey||ev.metaKey)&&ev.key==='k'){ev.preventDefault();openPal();return;}
  if(ev.key==='Escape'&&palOpen){closePal();return;}
  if(ev.key!=='Enter'||editing)return;
  var id=document.activeElement&&document.activeElement.id||'';
  if(id==='put-k'||id==='put-v')doPut();
  else if(id==='get-k')doGet();
  else if(id==='del-k')doDel();
  else if(id==='scan-s'||id==='scan-e')doScan();
});

/* ── INIT ── */
placeBar(0);
loadAll();
</script>
</body>
</html>`
