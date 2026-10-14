package monitoring

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// TPS/MSPT over RCON (spec §3.3, plan precision): the source is detected per server by trying
// `tick query` first and, for paper/purpur only, `mspt`. One command per sample keeps the
// server log at two RCON lines a minute.
const (
	cmdTickQuery = "tick query"
	cmdMSPT      = "mspt"

	detectAfter = 3
	maxTPS      = 1000
	maxMSPT     = 60000
	paperTarget = 20.0
)

// rcon-cli turns § codes into ANSI escapes (checked against Paper 26.2 on the test VM);
// other clients keep the § codes. Both are dropped before parsing.
var (
	formatCodeRe = regexp.MustCompile(`§.|\x1b\[[0-9;]*[A-Za-z]`)
	decimalComma = regexp.MustCompile(`(\d),(\d)`)
	tickRateRe   = regexp.MustCompile(`Target tick rate: (\d+(?:\.\d+)?) per second`)
	tickAvgRe    = regexp.MustCompile(`Average time per tick: (\d+(?:\.\d+)?)ms`)
	msptRe       = regexp.MustCompile(`from last 5s.*?(\d+(?:\.\d+)?)/(\d+(?:\.\d+)?)/(\d+(?:\.\d+)?)`)
)

type tpsReading struct {
	TPS  float64
	MSPT float64
}

// cleanRcon drops § and ANSI codes and turns decimal commas into points (DecimalFormat uses
// the JVM locale).
func cleanRcon(out string) string {
	return decimalComma.ReplaceAllString(formatCodeRe.ReplaceAllString(out, ""), "$1.$2")
}

func tpsValid(r tpsReading) (tpsReading, bool) {
	if r.TPS < 0 || r.TPS > maxTPS || r.MSPT < 0 || r.MSPT > maxMSPT {
		return tpsReading{}, false
	}
	return r, true
}

// parseTickQuery reads the answer of `tick query` (vanilla ≥ 1.20.3 and every server based on
// it). The status line and the rate lines may come without a separator.
func parseTickQuery(out string) (tpsReading, bool) {
	s := cleanRcon(out)
	rate, avg := tickRateRe.FindStringSubmatch(s), tickAvgRe.FindStringSubmatch(s)
	if rate == nil || avg == nil {
		return tpsReading{}, false
	}
	target, err1 := strconv.ParseFloat(rate[1], 64)
	mspt, err2 := strconv.ParseFloat(avg[1], 64)
	if err1 != nil || err2 != nil || target <= 0 || target > maxTPS {
		return tpsReading{}, false
	}
	r := tpsReading{TPS: target, MSPT: mspt}
	switch {
	case strings.Contains(s, "The game is frozen"):
		r.TPS = 0
	case mspt > 0:
		r.TPS = min(target, 1000/mspt)
	}
	return tpsValid(r)
}

// parseMSPT reads the first avg of Paper's `mspt` (the last 5 s); tps = min(20, 1000/mspt).
func parseMSPT(out string) (tpsReading, bool) {
	m := msptRe.FindStringSubmatch(strings.ReplaceAll(cleanRcon(out), "\n", " "))
	if m == nil {
		return tpsReading{}, false
	}
	mspt, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return tpsReading{}, false
	}
	r := tpsReading{TPS: paperTarget, MSPT: mspt}
	if mspt > 0 {
		r.TPS = min(paperTarget, 1000/mspt)
	}
	return tpsValid(r)
}

// tpsDetector remembers per server which command works. The zero value is ready; a new
// revision (version or type change) starts the detection over. The collector guards it.
type tpsDetector struct {
	revision int64
	fixed    string // "" while unknown
	tqGood   int
	tqBad    int
	msGood   int
	msBad    int
}

// command returns the next command to send; ok=false means this server has no TPS source.
func (d *tpsDetector) command(serverType string, revision int64) (string, bool) {
	if revision != d.revision {
		*d = tpsDetector{revision: revision}
	}
	paperish := serverType == string(instancespec.TypePaper) || serverType == string(instancespec.TypePurpur)
	switch {
	case d.fixed == TPSSourceTickQuery:
		return cmdTickQuery, true
	case d.fixed == TPSSourcePaper:
		return cmdMSPT, true
	case d.fixed == TPSSourceNone:
		return "", false
	case d.tqBad < detectAfter:
		return cmdTickQuery, true
	case paperish && d.msBad < detectAfter:
		return cmdMSPT, true
	default:
		d.fixed = TPSSourceNone
		return "", false
	}
}

// observe parses the answer of cmd and advances the detection.
func (d *tpsDetector) observe(cmd, out string) (tpsReading, bool) {
	var (
		r          tpsReading
		ok         bool
		good, bad  *int
		fixAsValid string
	)
	switch cmd {
	case cmdTickQuery:
		r, ok = parseTickQuery(out)
		good, bad, fixAsValid = &d.tqGood, &d.tqBad, TPSSourceTickQuery
	case cmdMSPT:
		r, ok = parseMSPT(out)
		good, bad, fixAsValid = &d.msGood, &d.msBad, TPSSourcePaper
	default:
		return tpsReading{}, false
	}
	if d.fixed == "" {
		if ok {
			*good++
			if *good >= detectAfter {
				d.fixed = fixAsValid
			}
		} else {
			*bad++
		}
	}
	return r, ok
}

func (d *tpsDetector) source() string {
	if d.fixed == "" {
		return TPSSourceUnknown
	}
	return d.fixed
}
