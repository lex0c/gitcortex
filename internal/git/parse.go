package git

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type NumstatEntry struct {
	Additions int64
	Deletions int64
}

type Totals struct {
	Additions int64
	Deletions int64
}

type RawEntry struct {
	Status  string
	OldHash string
	NewHash string
	PathOld string
	PathNew string
}

func parseNumstat(scanner *bufio.Scanner, discard *discardTracker) (map[string]NumstatEntry, Totals, error) {
	stats := make(map[string]NumstatEntry)
	var agg Totals

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			if err := discard.record("unexpected field count in numstat line"); err != nil {
				return nil, Totals{}, err
			}
			continue
		}

		add, err := parseInt64(fields[0])
		if err != nil {
			if err := discard.record(fmt.Sprintf("invalid additions value %q", fields[0])); err != nil {
				return nil, Totals{}, err
			}
			continue
		}

		del, err := parseInt64(fields[1])
		if err != nil {
			if err := discard.record(fmt.Sprintf("invalid deletions value %q", fields[1])); err != nil {
				return nil, Totals{}, err
			}
			continue
		}

		path := fields[len(fields)-1]
		stats[path] = NumstatEntry{Additions: add, Deletions: del}
		agg.Additions += add
		agg.Deletions += del
	}

	if err := scanner.Err(); err != nil {
		return nil, Totals{}, err
	}

	return stats, agg, nil
}

func parseRawChanges(reader *bufio.Reader, policy DiscardPolicy) ([]RawEntry, error) {
	discard := newDiscardTracker("raw", policy)
	var entries []RawEntry
	entryIndex := 0

	for {
		tok, err := readNULTerminated(reader)
		atEOF := errors.Is(err, io.EOF)
		if err != nil && !atEOF {
			return nil, err
		}
		entryIndex++

		if tok == "" || !strings.HasPrefix(tok, ":") {
			if atEOF {
				break
			}
			debugf("raw entry %d discarded: missing metadata prefix (token=%q)", entryIndex, tok)
			if err := discard.record("missing raw metadata prefix"); err != nil {
				return nil, err
			}
			continue
		}

		metaPathRaw := tok[1:]
		if !strings.Contains(metaPathRaw, "\t") {
			pathTok, pathErr := readNULTerminated(reader)
			pathAtEOF := errors.Is(pathErr, io.EOF)
			if pathErr != nil && !pathAtEOF {
				return nil, pathErr
			}
			if pathTok != "" && !strings.HasPrefix(pathTok, ":") {
				metaPathRaw = metaPathRaw + "\t" + pathTok
				atEOF = atEOF && pathAtEOF
			}
		}

		metaPath := strings.SplitN(metaPathRaw, "\t", 2)
		if len(metaPath) != 2 {
			if atEOF {
				break
			}
			debugf("raw entry %d discarded: unexpected meta/path format (token=%q)", entryIndex, tok)
			if err := discard.record("unexpected raw meta/path format"); err != nil {
				return nil, err
			}
			continue
		}

		metaParts := strings.Fields(metaPath[0])
		if len(metaParts) < 5 {
			if atEOF {
				break
			}
			debugf("raw entry %d discarded: insufficient metadata fields (meta=%v token=%q)", entryIndex, metaParts, tok)
			if err := discard.record("insufficient raw metadata fields"); err != nil {
				return nil, err
			}
			continue
		}

		status := metaParts[4]
		pathOld := metaPath[1]
		pathNew := pathOld

		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			pathTok, err := readNULTerminated(reader)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil, fmt.Errorf("unexpected EOF reading rename target for %s", pathOld)
				}
				return nil, err
			}
			pathNew = pathTok
		} else if status == "D" {
			pathNew = ""
		}

		entries = append(entries, RawEntry{
			Status:  status,
			OldHash: metaParts[2],
			NewHash: metaParts[3],
			PathOld: pathOld,
			PathNew: pathNew,
		})

		debugf("raw entry %d parsed: status=%s old=%s new=%s pathOld=%s pathNew=%s",
			entryIndex, status, metaParts[2], metaParts[3], pathOld, pathNew)

		if atEOF {
			break
		}
	}

	discard.finalize()
	return entries, nil
}

func readNULTerminated(r *bufio.Reader) (string, error) {
	tok, err := r.ReadString(0)
	if err != nil {
		if errors.Is(err, io.EOF) {
			if tok == "" {
				return "", io.EOF
			}
			return strings.TrimSuffix(tok, "\x00"), io.EOF
		}
		return "", err
	}
	return strings.TrimSuffix(tok, "\x00"), nil
}

func parseInt64(s string) (int64, error) {
	if s == "-" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}
