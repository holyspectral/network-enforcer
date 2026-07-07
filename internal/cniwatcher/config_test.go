package cniwatcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"secuity.rancher.io/network-enforcer/internal/cniwatcher"
	"secuity.rancher.io/network-enforcer/internal/types"
)

func TestParseCNIType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected types.CNIType
		wantErr  bool
	}{
		{
			name:     "Valid AWS VPC CNI type",
			input:    "aws-vpc",
			expected: types.CNITypeAWSVPC,
			wantErr:  false,
		},
		{
			name:     "Valid Calico CNI type",
			input:    "calico",
			expected: types.CNITypeCalico,
			wantErr:  false,
		},
		{
			name:     "Valid Cilium CNI type",
			input:    "cilium",
			expected: types.CNITypeCilium,
			wantErr:  false,
		},
		{
			name:     "Valid Flannel CNI type",
			input:    "flannel",
			expected: types.CNITypeFlannel,
			wantErr:  false,
		},
		{
			name:     "Empty CNI type",
			input:    "",
			expected: types.CNITypeUnknown,
			wantErr:  true,
		},
		{
			name:     "Invalid CNI type",
			input:    "invalid-cni",
			expected: types.CNITypeUnknown,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := cniwatcher.NewConfig("test-node", tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, config.CNIType)
			}
		})
	}
}

func TestNewConfig(t *testing.T) {
	tests := []struct {
		name             string
		nodeName         string
		cniType          string
		connEndpoint     string
		wantErr          bool
		expectedCNI      types.CNIType
		expectedEndpoint string
	}{
		{
			name:             "Valid AWS VPC config",
			nodeName:         "test-node",
			cniType:          "aws-vpc",
			wantErr:          false,
			expectedCNI:      types.CNITypeAWSVPC,
			expectedEndpoint: "",
		},
		{
			name:             "Valid Flannel config",
			nodeName:         "test-node",
			cniType:          "flannel",
			wantErr:          false,
			expectedCNI:      types.CNITypeFlannel,
			expectedEndpoint: "",
		},
		{
			name:             "Valid Calico config with default endpoint",
			nodeName:         "test-node",
			cniType:          "calico",
			connEndpoint:     "goldmane.calico-system.svc:7443",
			wantErr:          false,
			expectedCNI:      types.CNITypeCalico,
			expectedEndpoint: types.DefaultGoldmaneEndpoint,
		},
		{
			name:             "Valid Calico config with empty endpoint",
			nodeName:         "test-node",
			cniType:          "calico",
			connEndpoint:     "",
			wantErr:          false,
			expectedCNI:      types.CNITypeCalico,
			expectedEndpoint: types.DefaultGoldmaneEndpoint,
		},
		{
			name:             "Valid Cilium config with default endpoint",
			nodeName:         "test-node",
			cniType:          "cilium",
			connEndpoint:     "unix:///var/run/cilium/hubble.sock",
			wantErr:          false,
			expectedCNI:      types.CNITypeCilium,
			expectedEndpoint: types.DefaultHubbleEndpoint,
		},
		{
			name:             "Valid Cilium config with empty endpoint",
			nodeName:         "test-node",
			cniType:          "cilium",
			connEndpoint:     "",
			wantErr:          false,
			expectedCNI:      types.CNITypeCilium,
			expectedEndpoint: types.DefaultHubbleEndpoint,
		},
		{
			name:             "Empty node name",
			nodeName:         "",
			cniType:          "calico",
			wantErr:          true,
			expectedCNI:      types.CNITypeUnknown,
			expectedEndpoint: "",
		},
		{
			name:             "Invalid CNI type",
			nodeName:         "test-node",
			cniType:          "invalid-cni",
			wantErr:          true,
			expectedCNI:      types.CNITypeUnknown,
			expectedEndpoint: "",
		},
		{
			name:             "Unknown CNI type",
			nodeName:         "test-node",
			cniType:          "unknown",
			wantErr:          true,
			expectedCNI:      types.CNITypeUnknown,
			expectedEndpoint: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := cniwatcher.NewConfig(tt.nodeName, tt.cniType)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, cniwatcher.Config{}, config)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.nodeName, config.NodeName)
				assert.Equal(t, tt.expectedCNI, config.CNIType)
				assert.Equal(t, tt.expectedEndpoint, config.ConnEndpoint)
			}
		})
	}
}
