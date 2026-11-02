package tailer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCRI(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		wantStream string
		wantPart   bool
		wantText   string
		wantErr    bool
	}{
		{
			name:       "full stdout line",
			line:       "2026-10-29T13:00:00.000000001Z stdout F hello world",
			wantStream: "stdout",
			wantPart:   false,
			wantText:   "hello world",
		},
		{
			name:       "partial stderr line",
			line:       "2026-10-29T13:00:00.000000001Z stderr P chunk",
			wantStream: "stderr",
			wantPart:   true,
			wantText:   "chunk",
		},
		{
			name:       "empty content",
			line:       "2026-10-29T13:00:00.000000001Z stdout F ",
			wantStream: "stdout",
			wantText:   "",
		},
		{
			name:    "missing fields",
			line:    "2026-10-29T13:00:00Z stdout",
			wantErr: true,
		},
		{
			name:    "bad timestamp",
			line:    "not-a-time stdout F hello",
			wantErr: true,
		},
		{
			name:    "bad flag",
			line:    "2026-10-29T13:00:00.000000001Z stdout X hello",
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec, err := parseCRI([]byte(c.line))
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.wantStream, rec.Stream)
			assert.Equal(t, c.wantPart, rec.Partial)
			assert.Equal(t, c.wantText, string(rec.Text))
			assert.False(t, rec.Time.IsZero())
			assert.Equal(t, time.UTC, rec.Time.UTC().Location())
		})
	}
}

func TestCriJoiner(t *testing.T) {
	cases := []struct {
		name  string
		recs  []criRecord
		wantN int
		wantL []string
	}{
		{
			name:  "single full line",
			recs:  []criRecord{{Stream: "stdout", Text: []byte("abc")}},
			wantN: 1,
			wantL: []string{"abc"},
		},
		{
			name: "two partials then full",
			recs: []criRecord{
				{Stream: "stdout", Partial: true, Text: []byte("ab")},
				{Stream: "stdout", Partial: true, Text: []byte("cd")},
				{Stream: "stdout", Partial: false, Text: []byte("ef")},
			},
			wantN: 1,
			wantL: []string{"abcdef"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var j criJoiner
			var got []string
			for _, r := range c.recs {
				if out, ok := j.add(r); ok {
					got = append(got, string(out.Text))
				}
			}
			assert.Equal(t, c.wantN, len(got))
			assert.Equal(t, c.wantL, got)
		})
	}
}
