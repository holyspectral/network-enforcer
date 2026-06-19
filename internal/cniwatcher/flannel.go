package cniwatcher

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jdrews/go-tailer/fswatcher"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"secuity.rancher.io/network-enforcer/internal/types"
)

const (
	flannelLogPath = "/var/log/ulog/syslogemu.log"
)

const policySplitParts = 2

type FlannelWatcher struct {
	Watcher

	tailer fswatcher.FileTailer
}

// Regex to match DROP by policy lines and extract fields, with optional SPT/DPT for TCP/UDP.
var dropByPolicyRegex = regexp.MustCompile(
	`(?P<timestamp>^\w+\s+\d+\s+\d+:\d+:\d+)` +
		`\s+[^ ]+\s+DROP by policy (?P<policy>[\w-]+\/[\w-]+)` +
		` IN=[^ ]* OUT=[^ ]* MAC=[^ ]* SRC=(?P<srcip>[^ ]+) DST=(?P<dstip>[^ ]+)` +
		`.*?PROTO=(?P<proto>[^ ]+)` +
		`(?: SPT=(?P<srcport>\d+) DPT=(?P<dstport>\d+))?`,
)

func NewFlannelWatcher(watcher Watcher) (*FlannelWatcher, error) {
	return &FlannelWatcher{Watcher: watcher}, nil
}

func (w *FlannelWatcher) Start() error {
	w.Log.Info("Starting Flannel cniWatcher")

	tailer, err := w.CreateFileTailer(flannelLogPath)
	if err != nil {
		return fmt.Errorf("failed to create file tailer: %w", err)
	}
	w.tailer = tailer

	for {
		select {
		case <-w.Ctx.Done():
			w.Log.Info("Flannel cniWatcher shutting down due to context cancel")
			return nil
		case line := <-tailer.Lines():
			if line.Line == "" {
				continue
			}
			event := w.parsePolicyDenyEvent(line.Line)
			if processErr := w.ProcessPolicyDenyEvent(event); processErr != nil {
				w.Log.Error("failed to process policy deny event", "event", event, "err", processErr)
			}
		}
	}
}

// parsePolicyDenyEvent parses Flannel logs which are stored at /var/log/ulog/syslogemu.log
// It processes log lines with matching dropByPolicyRegex.
//
// The function may return (nil) when the log line is not a policy deny event (e.g., "ALLOW").
// This is not an error condition but indicates the line should be skipped.
//
// Returns:
//   - (*types.PolicyDenyEvent): Successfully parsed policy deny event
//   - (nil): Not a policy deny event (should be skipped)
func (w *FlannelWatcher) parsePolicyDenyEvent(line string) *types.PolicyDenyEvent {
	matches := dropByPolicyRegex.FindStringSubmatch(line)
	if matches == nil {
		// This is not a policy deny event, just skip it
		return nil
	}

	groupNames := dropByPolicyRegex.SubexpNames()
	fields := map[string]string{}
	for i, name := range groupNames {
		if i != 0 && name != "" {
			fields[name] = matches[i]
		}
	}

	// Parse timestamp (Flannel log does not have year, so use current year)
	timestampStr := fields["timestamp"]
	timeLayout := "Jan 2 15:04:05"
	t, err := time.Parse(timeLayout, timestampStr)
	if err != nil {
		w.Log.Error("failed to parse timestamp", "timestamp", timestampStr, "err", err)
		t = time.Now()
	} else {
		now := time.Now()
		t = t.AddDate(now.Year(), 0, 0)
	}

	policyParts := strings.SplitN(fields["policy"], "/", policySplitParts)
	var policyNamespace, policyName, policyKind string
	if len(policyParts) == policySplitParts {
		policyNamespace, policyName = policyParts[0], policyParts[1]
		policyKind = "NetworkPolicy"
	}

	srcIP := fields["srcip"]
	dstIP := fields["dstip"]
	proto := fields["proto"]

	srcPodOrServiceInfo, err := w.ResolvePodOrServiceByIP(srcIP)
	if err != nil {
		w.Log.Error("failed to get source por or service info", "ip", srcIP, "err", err)
	}
	dstPodOrServiceInfo, err := w.ResolvePodOrServiceByIP(dstIP)
	if err != nil {
		w.Log.Error("failed to get source por or service info", "ip", dstIP, "err", err)
	}

	apiVersion, err := w.GetNetworkPolicyAPIVersion(policyKind)
	if err != nil {
		w.Log.Error("Failed to get API version for policy",
			"policyKind", policyKind,
			"policyName", policyName,
			"policyNamespace", policyNamespace,
			"err", err)
	}

	// TODO: there is no reliable way to determine the enforce direction,
	//       use EgressEnforcedBy to report policy for now
	event := &types.PolicyDenyEvent{
		Timestamp:    t.Unix(),
		CNIType:      string(types.CNITypeFlannel),
		Protocol:     corev1.Protocol(proto),
		SrcNamespace: srcPodOrServiceInfo.Namespace,
		SrcName:      srcPodOrServiceInfo.Name,
		SrcLabels:    srcPodOrServiceInfo.Labels,
		DstNamespace: dstPodOrServiceInfo.Namespace,
		DstName:      dstPodOrServiceInfo.Name,
		DstLabels:    dstPodOrServiceInfo.Labels,
		EgressEnforcedBy: []types.Policy{{
			TypeMeta:  metav1.TypeMeta{APIVersion: apiVersion, Kind: policyKind},
			Name:      policyName,
			Namespace: policyNamespace,
		}},
	}

	return event
}

func (w *FlannelWatcher) Shutdown() error {
	if w.tailer != nil {
		w.tailer.Close()
	}

	return nil
}
