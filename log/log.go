// Custom logger for gotrace
package log

import (
	"log"
)

func logf(format string, vars ...interface{}) {
	log.Printf("[ERRGOTRACE] " + format + "\n", vars...)
}

func InspectReturnValues(f string, vars ...interface{}) {
	for _, v := range vars {
		if err, ok := v.(error); ok && err != nil{
			logf("%s: %s", f, err.Error());
		}
	}
}

func Setup() bool {
	return true
}
