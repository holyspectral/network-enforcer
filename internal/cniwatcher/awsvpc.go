package cniwatcher

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jdrews/go-tailer/fswatcher"
	"github.com/rancher-sandbox/network-enforcer/internal/types"
	corev1 "k8s.io/api/core/v1"
)

const (
	awsVPCLogPath = "/var/log/aws-routed-eni/network-policy-agent.log"
)

type AWSVPCWatcher struct {
	Watcher

	tailer fswatcher.FileTailer
}

type flowLog struct {
	Level     string `json:"level"`
	Timestamp string `json:"ts"`
	Logger    string `json:"logger"`
	Message   string `json:"msg"`
	SrcIP     string `json:"Src IP"`
	SrcPort   int    `json:"Src Port"`
	DestIP    string `json:"Dest IP"`
	DestPort  int    `json:"Dest Port"`
	Proto     string `json:"Proto"`
	Verdict   string `json:"Verdict"`
}

func NewAWSVPCWatcher(watcher Watcher) (*AWSVPCWatcher, error) {
	return &AWSVPCWatcher{Watcher: watcher}, nil
}

func (w *AWSVPCWatcher) Start() error {
	w.Log.Info("Starting AWS VPC cniWatcher")

	tailer, err := w.CreateFileTailer(awsVPCLogPath)
	if err != nil {
		return fmt.Errorf("failed to create file tailer: %w", err)
	}
	w.tailer = tailer

	for {
		select {
		case <-w.Ctx.Done():
			w.Log.Info("AWS VPC cniWatcher shutting down due to context cancel")
			return nil
		case line := <-tailer.Lines():
			if line.Line == "" {
				continue
			}
			event, parseErr := w.parsePolicyDenyEvent(line.Line)
			if parseErr != nil {
				w.Log.Error("failed to parse log line", "line", line.Line, "err", parseErr)
				continue
			}
			if processErr := w.ProcessPolicyDenyEvent(event); processErr != nil {
				w.Log.Error("failed to process policy deny event", "event", event, "err", processErr)
			}
		}
	}
}

// parsePolicyDenyEvent parses AWS VPC network policy logs which are stored at /var/log/aws-routed-eni/network-policy-agent.log
// It unmarshals the JSON log line into a flowLog struct and processes only events with "DENY" verdict.
//
// The function may return (nil, nil) when the log line is not a policy deny event (e.g., "ALLOW" verdict).
// This is not an error condition but indicates the line should be skipped.
//
// Returns:
//   - (*types.PolicyDenyEvent, nil): Successfully parsed policy deny event
//   - (nil, error): Failed to parse the log line (malformed JSON, etc.)
//   - (nil, nil): Not a policy deny event (should be skipped)
func (w *AWSVPCWatcher) parsePolicyDenyEvent(line string) (*types.PolicyDenyEvent, error) {
	var flow flowLog
	if err := json.Unmarshal([]byte(line), &flow); err != nil {
		return nil, fmt.Errorf("failed to unmarshal log line: %w", err)
	}

	if flow.Verdict != "DENY" {
		return nil, nil //nolint:nilnil // This is not a policy deny event, just skip it
	}

	timestamp, err := time.Parse(time.RFC3339Nano, flow.Timestamp)
	if err != nil {
		w.Log.Warn("failed to parse timestamp", "err", err, "timestamp", flow.Timestamp)
		timestamp = time.Now()
	}

	srcPodOrServiceInfo, err := w.ResolvePodOrServiceByIP(flow.SrcIP)
	if err != nil {
		w.Log.Error("failed to get source por or service info", "ip", flow.SrcIP, "err", err)
	}

	dstPodOrServiceInfo, err := w.ResolvePodOrServiceByIP(flow.DestIP)
	if err != nil {
		w.Log.Error("failed to get destination por or service info", "ip", flow.DestIP, "err", err)
	}

	event := &types.PolicyDenyEvent{
		Timestamp:    timestamp.Unix(),
		CNIType:      string(types.CNITypeAWSVPC),
		Protocol:     corev1.Protocol(flow.Proto),
		SrcNamespace: srcPodOrServiceInfo.Namespace,
		SrcName:      srcPodOrServiceInfo.Name,
		SrcLabels:    srcPodOrServiceInfo.Labels,
		DstNamespace: dstPodOrServiceInfo.Namespace,
		DstName:      dstPodOrServiceInfo.Name,
		DstLabels:    dstPodOrServiceInfo.Labels,
	}

	return event, nil
}

func (w *AWSVPCWatcher) Shutdown() error {
	if w.tailer != nil {
		w.tailer.Close()
	}

	return nil
}
