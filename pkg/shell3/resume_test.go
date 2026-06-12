package shell3

import "testing"

func TestSpec_HasResumeAndParentFields(t *testing.T) {
	var s Spec
	s.ResumeID = 7
	s.ParentSession = 3
	if s.ResumeID != 7 || s.ParentSession != 3 {
		t.Fatal("unreachable")
	}
}
