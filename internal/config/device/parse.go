package device

import (
	"fmt"
	"strings"
)

// ParseDevice parses device mapping string to a src, dest & permissions string.
func parseDevice(device string) (src, dst, permissions string, err error) {
	permissions = "rwm"

	arr := strings.Split(device, ":")
	switch len(arr) {
	case 3:
		if !isValidDeviceMode(arr[2]) {
			return "", "", "", fmt.Errorf("invalid device mode: %s", arr[2])
		}

		permissions = arr[2]

		fallthrough
	case 2:
		if isValidDeviceMode(arr[1]) {
			permissions = arr[1]
		} else {
			if arr[1] != "" && arr[1][0] != '/' {
				return "", "", "", fmt.Errorf("invalid device mode: %s", arr[1])
			}

			dst = arr[1]
		}

		fallthrough
	case 1:
		src = arr[0]
	default:
		return "", "", "", fmt.Errorf("invalid device specification: %s", device)
	}

	if dst == "" {
		dst = src
	}

	return src, dst, permissions, nil
}

// IsValidDeviceMode checks if the mode for device is valid or not.
// IsValid mode is a composition of r (read), w (write), and m (mknod).
func isValidDeviceMode(mode string) bool {
	legalDeviceMode := map[rune]bool{
		'r': true,
		'w': true,
		'm': true,
	}

	if mode == "" {
		return false
	}

	for _, c := range mode {
		if !legalDeviceMode[c] {
			return false
		}

		legalDeviceMode[c] = false
	}

	return true
}
