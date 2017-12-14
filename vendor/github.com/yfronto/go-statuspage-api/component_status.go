package statuspage

import (
	"strings"
)

type ComponentStatus int

/*
 *  http://doers.statuspage.io/api/v1/components/
 *  operational|degraded_performance|partial_outage|major_outage
 */

const (
	Operational ComponentStatus = iota
	DegradedPerformance
	PartialOutage
	MajorOutage
)

func (i ComponentStatus) ToLower() string {
	return strings.ToLower(i.String())
}
