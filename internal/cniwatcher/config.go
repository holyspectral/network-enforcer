package cniwatcher

import (
	"errors"
	"fmt"
	"os"

	"github.com/rancher-sandbox/network-enforcer/internal/types"
)

func parseCNIType(value string) (types.CNIType, error) {
	switch types.CNIType(value) {
	case types.CNITypeAWSVPC:
		return types.CNITypeAWSVPC, nil
	case types.CNITypeCalico:
		return types.CNITypeCalico, nil
	case types.CNITypeCilium:
		return types.CNITypeCilium, nil
	case types.CNITypeFlannel:
		return types.CNITypeFlannel, nil
	case types.CNITypeUnknown:
		fallthrough
	default:
		return types.CNITypeUnknown, fmt.Errorf("unsupported CNI type: %q", value)
	}
}

type Config struct {
	NodeName     string
	CNIType      types.CNIType
	ConnEndpoint string
}

func NewConfig(nodeName, cniType string) (Config, error) {
	if nodeName == "" {
		return Config{}, errors.New("NodeName must be set")
	}

	parsedCNIType, err := parseCNIType(cniType)
	if err != nil {
		return Config{}, err
	}

	config := Config{
		NodeName: nodeName,
		CNIType:  parsedCNIType,
	}

	if parsedCNIType == types.CNITypeCalico {
		config.ConnEndpoint = os.Getenv("CNIWATCHER_GOLDMANE_ENDPOINT")
		if config.ConnEndpoint == "" {
			config.ConnEndpoint = types.DefaultGoldmaneEndpoint
		}
	}
	if parsedCNIType == types.CNITypeCilium {
		config.ConnEndpoint = os.Getenv("CNIWATCHER_HUBBLE_ENDPOINT")
		if config.ConnEndpoint == "" {
			config.ConnEndpoint = types.DefaultHubbleEndpoint
		}
	}

	return config, nil
}
