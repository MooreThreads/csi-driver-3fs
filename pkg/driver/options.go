package driver

import (
	"fmt"
	"strconv"

	"github.com/MooreThreads/csi-driver-3fs/pkg/state"
)

// ignoreFailedReadParameterName is a parameter that, when set to true,
// causes the `--ignore-failed-read` option to be passed to `tar`.
const ignoreFailedReadParameterName = "ignoreFailedRead"

func optionsFromParameters(vol state.Volume, parameters map[string]string) ([]string, error) {
	// We do not support options for snapshots of block volumes
	if vol.VolAccessType == state.BlockAccess {
		return nil, nil
	}

	ignoreFailedReadString := parameters[ignoreFailedReadParameterName]
	if len(ignoreFailedReadString) == 0 {
		return nil, nil
	}

	if ok, err := strconv.ParseBool(ignoreFailedReadString); err != nil {
		return nil, fmt.Errorf(
			"invalid value for %q, expected boolean but was %q",
			ignoreFailedReadParameterName,
			ignoreFailedReadString,
		)
	} else if ok {
		return []string{"--ignore-failed-read"}, nil
	}

	return nil, nil
}
