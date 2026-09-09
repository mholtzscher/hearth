package zigbee2mqtt

import "errors"

// This module classifies Zigbee2MQTT adapter startup failures by stage so the
// executable can report process.failed with the failed stage without logging
// configuration values or upstream connection details.

// StageLoadProfileCatalog names the startup stage that compiles the embedded
// profile catalog before any NATS or MQTT connection.
const StageLoadProfileCatalog = "load_profile_catalog"

// ErrorCodeProfileCatalogInvalid is the executable error code reported when
// the embedded profile catalog fails to compile.
const ErrorCodeProfileCatalogInvalid = "profile_catalog_invalid"

// ErrorCodeRunFailed is the executable error code reported when startup fails
// outside a classified stage.
const ErrorCodeRunFailed = "run_failed"

// runStageError identifies the startup stage that failed without echoing
// configuration values or upstream connection details.
type runStageError struct {
	stage string
	err   error
}

func (err *runStageError) Error() string {
	return "zigbee2mqtt stage " + err.stage + " failed: " + err.err.Error()
}

func (err *runStageError) Unwrap() error { return err.err }

// ErrorStage reports the failed startup stage carried by err, or "run" when
// the error carries no stage. Executables use it for the process.failed stage
// field without logging the underlying error text.
func ErrorStage(err error) string {
	if stageErr, ok := errors.AsType[*runStageError](err); ok {
		return stageErr.stage
	}
	return "run"
}

// ErrorCode reports the executable error code for err:
// profile_catalog_invalid when the embedded profile catalog failed to load,
// run_failed otherwise. Executables use it for the process.failed error_code
// field without logging the underlying error text.
func ErrorCode(err error) string {
	if ErrorStage(err) == StageLoadProfileCatalog {
		return ErrorCodeProfileCatalogInvalid
	}
	return ErrorCodeRunFailed
}

func failStage(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &runStageError{stage: stage, err: err}
}
