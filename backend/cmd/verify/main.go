// Command verify exercises every feature (F-1..F-7) and hard task
// (T-1..T-5) end to end through the API of a running, seeded system and
// prints a PASS/FAIL table. It exits non-zero if anything fails.
//
//	docker compose --profile verify run --rm verify      (make verify)
//
// Each run creates its own employees (employee numbers V<timestamp>), so
// it can be repeated; the payroll workflow uses the earliest payroll month
// that is not closed yet. `make reset-demo` restores the initial data.
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type check struct {
	id, name string
	run      func(*env) error
}

func main() {
	base := os.Getenv("VERIFY_BASE_URL")
	if base == "" {
		base = "http://api:8080"
	}
	password := os.Getenv("DEMO_PASSWORD")
	if password == "" {
		password = "Demo.2026!"
	}
	e := &env{base: base, password: password, runID: time.Now().UTC().Format("0102150405")}

	fmt.Printf("BordroPlus doğrulama — %s (çalıştırma %s)\n\n", base, e.runID)
	if err := e.waitReady(5 * time.Minute); err != nil {
		fmt.Println("Sistem hazır değil:", err)
		os.Exit(2)
	}

	checks := []check{
		{"HEALTH", "GET /health ve veritabanı bağlantısı", (*env).checkHealth},
		{"AUTH", "Giriş, yanlış parola, refresh rotation, çıkış", (*env).checkAuth},
		{"AUTHZ", "Token yok 401, yetkisiz rol 403, sahte token", (*env).checkAuthz},
		{"F-1", "Personel kartı ve organizasyon ağacı", (*env).checkEmployees},
		{"IDEMP", "Idempotency-Key ile tekrar edilen POST", (*env).checkIdempotency},
		{"T-5", "Alt ağaç yetkisi, vekâlet, maaş maskeleme", (*env).checkScope},
		{"T-1", "Hakediş, bakiye, eşzamanlı onay, rapor iadesi", (*env).checkLeaveEngine},
		{"F-2", "İzin talebi, onay kuyruğu ve bakiye", (*env).checkLeaveFlow},
		{"T-2", "Eksik/çift basım, gece vardiyası, öneri onayı", (*env).checkTimesheet},
		{"F-3", "Puantaj ay onayı ve kilit", (*env).checkTimesheetMonth},
		{"T-3", "Brütten nete, kümülatif matrah, geçmiş ay düzeltmesi", (*env).checkPayrollEngine},
		{"T-4", "Paralel hesap <10 sn, determinizm, iş akışı", (*env).checkPayrollWorkflow},
		{"F-4", "Bordro hesaplama ve onay akışı (rol ayrımı)", (*env).checkPayrollApproval},
		{"F-6", "Dönem kapanışı ve kapalı dönem koruması", (*env).checkPeriodClose},
		{"F-5", "Avans/borç/ek ödeme ve geçmiş dönem düzeltmesi", (*env).checkItemsAndRetro},
		{"F-7", "Raporlar, CSV ve bordro çıktısı", (*env).checkReports},
		{"AUDIT", "Denetim kaydı (yalnız denetçi)", (*env).checkAudit},
	}

	type result struct {
		check
		err error
		dur time.Duration
	}
	var results []result
	failed := 0
	for _, c := range checks {
		start := time.Now()
		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			return c.run(e)
		}()
		results = append(results, result{c, err, time.Since(start)})
		if err != nil {
			failed++
		}
	}

	fmt.Printf("%-7s %-56s %-5s %8s\n", "KİMLİK", "KONTROL", "SONUÇ", "SÜRE")
	fmt.Println(strings.Repeat("─", 80))
	for _, r := range results {
		status := "PASS"
		if r.err != nil {
			status = "FAIL"
		}
		name := r.name
		if n := utf8.RuneCountInString(name); n < 56 {
			name += strings.Repeat(" ", 56-n)
		}
		fmt.Printf("%-7s %s %-5s %7dms\n", r.id, name, status, r.dur.Milliseconds())
		if r.err != nil {
			fmt.Printf("        └─ %v\n", r.err)
		}
	}
	fmt.Println(strings.Repeat("─", 80))
	fmt.Printf("%d/%d PASS\n", len(results)-failed, len(results))
	for _, n := range e.notes {
		fmt.Println("·", n)
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// env carries the clients and what earlier checks created.
type env struct {
	base, password, runID string
	users                 map[string]*client
	notes                 []string

	unitID    map[string]int64
	verifyEmp int64 // created in F-1: 65.000 TL from 2025-01-01, hired 2024-01-02
	minWage   int64 // minimum wage employee for T-3
	period    time.Time
}

func (e *env) note(format string, args ...any) {
	e.notes = append(e.notes, fmt.Sprintf(format, args...))
}

func expect(ok bool, format string, args ...any) error {
	if ok {
		return nil
	}
	return fmt.Errorf(format, args...)
}

var errPrereq = errors.New("önceki adım başarısız olduğu için çalıştırılamadı")

func (e *env) u(local string) *client { return e.users[local] }

func (e *env) waitReady(limit time.Duration) error {
	deadline := time.Now().Add(limit)
	anon := newClient(e.base)
	for {
		r, err := anon.do(http.MethodGet, "/health", nil)
		if err == nil && r.status == 200 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("/health yanıt vermiyor: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
	e.users = map[string]*client{}
	for _, local := range []string{"calisan", "yonetici", "ik", "bordro", "muhasebe", "denetci"} {
		c := newClient(e.base)
		if err := c.login(local+"@bordroplus.test", e.password); err != nil {
			return fmt.Errorf("demo hesabı %s: %w", local, err)
		}
		e.users[local] = c
	}
	// The simulator's history must be loaded before payroll checks.
	for {
		r, err := e.u("ik").do(http.MethodGet, "/api/v1/system/status", nil)
		if err == nil && r.status == 200 && r.body["simulator"] != nil {
			sim := r.body["simulator"].(map[string]any)
			e.note("simülatör: %.0f çalışan, %.0f basım, tohum %.0f", sim["employees"], sim["punches_loaded"], sim["seed"])
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("simülatör geçmiş veriyi yüklemedi")
		}
		time.Sleep(3 * time.Second)
	}
}

func path(format string, args ...any) string { return "/api/v1" + fmt.Sprintf(format, args...) }

// parallel runs fns at the same time and returns their errors.
func parallel(fns ...func() error) []error {
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = fn()
		}()
	}
	close(start)
	wg.Wait()
	return errs
}
