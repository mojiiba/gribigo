package server

import (
	"fmt"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	aftpb "github.com/openconfig/gribi/v1/proto/gribi_aft"
	spb "github.com/openconfig/gribi/v1/proto/service"
	"github.com/openconfig/gribigo/rib"
	wpb "github.com/openconfig/ygot/proto/ywrapper"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		desc        string
		inIDs       []string
		wantClients map[string]*clientState
		// wantErrorCode is a map of the operation sequence (zero-indexed)
		// to an error code that is expected.
		wantClientErrCode map[int]codes.Code
	}{{
		desc:  "successfully create single client",
		inIDs: []string{"c1"},
		wantClients: map[string]*clientState{
			"c1": {params: &clientParams{}},
		},
	}, {
		desc:  "fail to create duplicate client",
		inIDs: []string{"c1", "c1"},
		wantClients: map[string]*clientState{
			"c1": {params: &clientParams{}},
		},
		wantClientErrCode: map[int]codes.Code{
			1: codes.Internal,
		},
	}, {
		desc:  "create multiple clients",
		inIDs: []string{"c1", "c2"},
		wantClients: map[string]*clientState{
			"c1": {params: &clientParams{}},
			"c2": {params: &clientParams{}},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			s := New(nil)
			for i, c := range tt.inIDs {
				wantErr := tt.wantClientErrCode[i]
				gotErr := s.newClient(c)
				switch {
				case gotErr == nil && wantErr != codes.OK:
					t.Fatalf("did not get expected error for index %d, got nil", i)
				case !cmp.Equal(wantErr, status.Code(gotErr)):
					t.Fatalf("did not get expected error code, got: %v (%v), want: %v", status.Code(gotErr), gotErr, wantErr)
				}
			}
			if diff := cmp.Diff(tt.wantClients, s.cs,
				cmp.AllowUnexported(clientState{})); diff != "" {
				t.Fatalf("did not get expected clients, diff(-want,+got):\n%s", diff)
			}
		})
	}
}

func TestUpdateParams(t *testing.T) {
	tests := []struct {
		desc        string
		inServer    *Server
		inID        string
		inParams    *spb.SessionParameters
		wantCode    codes.Code
		wantDetails *spb.ModifyRPCErrorDetails
		wantState   *clientParams
	}{{
		desc:     "uninitialised client",
		inServer: &Server{},
		inID:     "c1",
		inParams: &spb.SessionParameters{},
		wantCode: codes.Internal,
	}, {
		desc: "client with previously set state",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params:    &clientParams{},
					setParams: true,
				},
			},
		},
		inID:     "c1",
		inParams: &spb.SessionParameters{},
		wantCode: codes.FailedPrecondition,
		wantDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_MODIFY_NOT_ALLOWED,
		},
	}, {
		desc: "new client, with all fields as default",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{},
				},
			},
		},
		inID:     "c1",
		inParams: &spb.SessionParameters{},
		wantState: &clientParams{
			ExpectElecID: false,
			Persist:      false,
			FIBAck:       false,
		},
	}, {
		desc: "new client, with all fields non-default",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{},
				},
			},
		},
		inID: "c1",
		inParams: &spb.SessionParameters{
			Persistence: spb.SessionParameters_PRESERVE,
			Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
			AckType:     spb.SessionParameters_RIB_AND_FIB_ACK,
		},
		wantState: &clientParams{
			ExpectElecID: true,
			Persist:      true,
			FIBAck:       true,
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if err := tt.inServer.updateParams(tt.inID, tt.inParams); err != nil {
				p, ok := status.FromError(err)
				if !ok {
					t.Fatalf("did not get expected error, got: %v but was not a status.Status", err)
				}

				if got, want := p.Code(), tt.wantCode; got != want {
					t.Errorf("did not get expected error code, got: %v, want: %v (error: %v)", got, want, err)
				}

				if got, want := p.Proto().Details, tt.wantDetails; tt.wantDetails != nil {
					if l := len(got); l != 1 {
						t.Fatalf("did not get expected error details, got %d messages, expected 1", l)
					}

					gotErrDet := &spb.ModifyRPCErrorDetails{}
					if err := got[0].UnmarshalTo(gotErrDet); err != nil {
						t.Fatalf("did not get expected error type, got: %T, unmarshal error: %v", got[0], err)
					}

					if diff := cmp.Diff(gotErrDet, want, protocmp.Transform()); diff != "" {
						t.Fatalf("did not got expected details, %s", diff)
					}
				}
				return
			}

			if diff := cmp.Diff(tt.inServer.cs[tt.inID].params, tt.wantState); diff != "" {
				t.Fatalf("did not get expected state, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestCheckClientsConsistent(t *testing.T) {
	tests := []struct {
		desc     string
		inServer *Server
		inID     string
		inParams *clientParams
		want     bool
		wantErr  bool
	}{{
		desc: "inconsistent clients",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: true,
					},
				},
			},
		},
		inParams: &clientParams{},
		inID:     "c2",
		want:     false,
	}, {
		desc: "consistent clients",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: true,
					},
				},
			},
		},
		inParams: &clientParams{
			ExpectElecID: true,
		},
		inID: "c2",
		want: true,
	}, {
		desc: "nil parameters for existing client",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {},
			},
		},
		inParams: &clientParams{},
		inID:     "c2",
		wantErr:  true,
	}, {
		desc:     "nil parameters for new client",
		inServer: &Server{},
		inID:     "error",
		wantErr:  true,
	}, {
		desc: "skip this client",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {},
			},
		},
		inParams: &clientParams{},
		inID:     "c1",
		want:     true,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := tt.inServer.checkClientsConsistent(tt.inID, tt.inParams)
			if (err != nil) != tt.wantErr {
				t.Fatalf("did not get expected error, got err: %v, wantErr? %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("did not get expected consistency value, got: %v, want: %v", got, tt.want)
			}
		})
	}
}
func TestCheckParams(t *testing.T) {
	tests := []struct {
		desc           string
		inID           string
		inServer       *Server
		inParams       *spb.SessionParameters
		inGotMsg       bool
		wantResponse   *spb.ModifyResponse
		wantErrCode    codes.Code
		wantErrDetails *spb.ModifyRPCErrorDetails
	}{{
		desc: "already received message",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		inID:        "c1",
		inGotMsg:    true,
		inParams:    &spb.SessionParameters{},
		wantErrCode: codes.FailedPrecondition,
		wantErrDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_MODIFY_NOT_ALLOWED,
		},
	}, {
		desc: "invalid combination",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		inID: "c1",
		inParams: &spb.SessionParameters{
			Persistence: spb.SessionParameters_PRESERVE,
			Redundancy:  spb.SessionParameters_ALL_PRIMARY,
		},
		wantErrCode: codes.FailedPrecondition,
		wantErrDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		},
		// TODO(robjs): test invalid combination, since today we do not actually support >1 mode, we cannot
		// test this directly.

	}, {
		desc: "ALL_PRIMARY unsupported",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		inID: "c1",
		inParams: &spb.SessionParameters{
			Redundancy: spb.SessionParameters_ALL_PRIMARY,
		},
		wantErrCode: codes.Unimplemented,
		wantErrDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		},
	}, {
		desc: "nil parameters",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		wantErrCode: codes.Internal,
	}, {
		desc: "delete persistence unsupported",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		inParams: &spb.SessionParameters{
			Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
			Persistence: spb.SessionParameters_DELETE,
		},
		wantErrCode: codes.Unimplemented,
		wantErrDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		},
	}, {
		desc: "received OK message",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {params: &clientParams{}},
			},
		},
		inID: "c1",
		inParams: &spb.SessionParameters{
			Redundancy:  spb.SessionParameters_SINGLE_PRIMARY,
			Persistence: spb.SessionParameters_PRESERVE,
		},
		wantResponse: &spb.ModifyResponse{
			SessionParamsResult: &spb.SessionParametersResult{
				Status: spb.SessionParametersResult_OK,
			},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			s := tt.inServer
			got, err := s.checkParams(tt.inID, tt.inParams, tt.inGotMsg)
			if err != nil {
				st, ok := status.FromError(err)
				if !ok {
					t.Fatalf("got error that was not a status.Status, got: %v", err)
				}
				if st.Code() != tt.wantErrCode {
					t.Fatalf("did not get expected code, got: %s, want: %s, error: %v", st.Code(), tt.wantErrCode, err)
				}

				if errS := chkErrDetails(st, tt.wantErrDetails); errS != "" {
					t.Fatalf(errS)
				}

				return
			}

			if diff := cmp.Diff(got, tt.wantResponse, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected error, diff(-got,+want):\n%s", diff)
			}
		})
	}
}

func TestIsNewMaster(t *testing.T) {
	tests := []struct {
		desc       string
		inCand     *spb.Uint128
		inExist    *spb.Uint128
		wantMaster bool
		wantEqual  bool
		wantErr    bool
	}{{
		desc:       "new master - low only",
		inCand:     &spb.Uint128{Low: 2},
		inExist:    &spb.Uint128{Low: 1},
		wantMaster: true,
	}, {
		desc:       "new master - high only",
		inCand:     &spb.Uint128{High: 2},
		inExist:    &spb.Uint128{High: 1},
		wantMaster: true,
	}, {
		desc:       "new master - high and low",
		inCand:     &spb.Uint128{High: 4, Low: 3},
		inExist:    &spb.Uint128{High: 4, Low: 2},
		wantMaster: true,
	}, {
		desc:      "equal",
		inCand:    &spb.Uint128{High: 42, Low: 42},
		inExist:   &spb.Uint128{High: 42, Low: 42},
		wantEqual: true,
	}, {
		desc:       "nil input",
		inCand:     &spb.Uint128{High: 4242, Low: 4242},
		wantMaster: true,
	}, {
		desc:    "not master",
		inCand:  &spb.Uint128{High: 1, Low: 1},
		inExist: &spb.Uint128{High: 44, Low: 42},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			gotMaster, gotEqual, err := isNewMaster(tt.inCand, tt.inExist)

			if got, want := (err != nil), tt.wantErr; got != want {
				t.Fatalf("did not get expected error, gotErr: %v, want: %v", err, want)
			}

			if got, want := gotMaster, tt.wantMaster; got != want {
				t.Errorf("did not get expected master result, got: %v, want: %v", got, want)
			}

			if got, want := gotEqual, tt.wantEqual; got != want {
				t.Errorf("did not get expected equal result, got: %v, want: %v", got, want)
			}
		})
	}
}

// chkErrDetails is a helper to check whether a status contains an expected ModifyRPCErrorDetails.
func chkErrDetails(st *status.Status, d *spb.ModifyRPCErrorDetails) string {
	if d == nil {
		// skip check if it's not expected
		return ""
	}
	if got := len(st.Details()); got != 1 {
		return fmt.Sprintf("did not get expected number of details arguments, got: %d (%v), want: 1", got, st.Details())
	}
	if got, want := st.Details()[0], d; !cmp.Equal(got, want, protocmp.Transform()) {
		return fmt.Sprintf("did not get expected error details, got: %s, want: %s", got, want)
	}
	return ""
}

func TestRunElection(t *testing.T) {
	tests := []struct {
		desc             string
		inServer         *Server
		inID             string
		inElecID         *spb.Uint128
		wantResponse     *spb.ModifyResponse
		wantServerElecID *spb.Uint128
		wantServerMaster string
		wantErrCode      codes.Code
		wantErrDetails   *spb.ModifyRPCErrorDetails
	}{{
		desc: "becomes master - no election ID",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: true,
					}},
			},
		},
		inID:     "c1",
		inElecID: &spb.Uint128{High: 0, Low: 1},
		wantResponse: &spb.ModifyResponse{
			ElectionId: &spb.Uint128{High: 0, Low: 1},
		},
		wantServerElecID: &spb.Uint128{High: 0, Low: 1},
		wantServerMaster: "c1",
	}, {
		desc: "becomes master - election ID present",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: true,
					}},
			},
			curElecID: &spb.Uint128{
				High: 0,
				Low:  1,
			},
		},
		inID:     "c1",
		inElecID: &spb.Uint128{High: 0, Low: 2},
		wantResponse: &spb.ModifyResponse{
			ElectionId: &spb.Uint128{High: 0, Low: 2},
		},
		wantServerElecID: &spb.Uint128{High: 0, Low: 2},
		wantServerMaster: "c1",
	}, {
		desc: "does not become master",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: true,
					},
				},
			},
			curElecID: &spb.Uint128{
				High: 0,
				Low:  4000,
			},
			curMaster: "existing",
		},
		inID:     "c1",
		inElecID: &spb.Uint128{High: 0, Low: 2},
		wantResponse: &spb.ModifyResponse{
			ElectionId: &spb.Uint128{High: 0, Low: 4000},
		},
		wantServerElecID: &spb.Uint128{High: 0, Low: 4000},
		wantServerMaster: "existing",
	}, {
		desc: "not expecting election",
		inServer: &Server{
			cs: map[string]*clientState{
				"c1": {
					params: &clientParams{
						ExpectElecID: false,
					},
				},
			},
		},
		inID:        "c1",
		wantErrCode: codes.FailedPrecondition,
		wantErrDetails: &spb.ModifyRPCErrorDetails{
			Reason: spb.ModifyRPCErrorDetails_ELECTION_ID_IN_ALL_PRIMARY,
		},
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			s := tt.inServer
			got, err := s.runElection(tt.inID, tt.inElecID)
			if err != nil {
				st, ok := status.FromError(err)
				if !ok {
					t.Fatalf("returned error that was not a status.Status, got:%v", err)
				}
				if got, want := st.Code(), tt.wantErrCode; got != want {
					t.Fatalf("did not get expected error code, got: %s, want: %s (error: %v)", got, want, err)
				}

				if errS := chkErrDetails(st, tt.wantErrDetails); errS != "" {
					t.Fatalf(errS)
				}

				return
			}

			if diff := cmp.Diff(got, tt.wantResponse, protocmp.Transform()); diff != "" {
				t.Errorf("did not get expected response, diff(-got,+want):\n%s", diff)
			}

			if got, want := s.curElecID, tt.wantServerElecID; !cmp.Equal(got, want, protocmp.Transform()) {
				t.Errorf("did not get expected server ID, got: %s, want: %s", got, want)
			}

			if got, want := s.curMaster, tt.wantServerMaster; !cmp.Equal(got, want) {
				t.Errorf("did not get expected master ID, got: %s, want: %s", got, want)
			}
		})
	}
}

func checkStatusErr(t *testing.T, err error, wantCode codes.Code, wantReason spb.ModifyRPCErrorDetails_Reason) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("got an error that was not a status, %v", err)
	}
	if st.Code() != wantCode {
		t.Fatalf("did not get expected code, got: %s (%s), want: %s", st.Code(), st.Proto(), wantCode)
	}

	if wantReason != 0 {
		if g := len(st.Details()); g != 1 {
			t.Fatalf("did not get expected details, got: %d entries, want: 1", g)
		}

		dets, ok := st.Details()[0].(*spb.ModifyRPCErrorDetails)
		if !ok {
			t.Fatalf("got bad proto in details, got: %T, want: *spb.ModifyRPCErrorDetails", st.Details()[0])
		}
		if got, want := dets.Reason, wantReason; got != want {
			t.Fatalf("did not get expected reason, got: %s, want: %s", got, want)
		}
	}
}

func TestDoModify(t *testing.T) {
	type expectedMsg struct {
		result    *spb.ModifyResponse
		errCode   codes.Code
		errReason spb.ModifyRPCErrorDetails_Reason
	}
	tests := []struct {
		desc     string
		inCID    string
		inServer *Server
		inOps    []*spb.AFTOperation
		wantMsg  []*expectedMsg
	}{{
		desc:     "unknown client",
		inServer: New(),
		inCID:    "unknown",
		wantMsg: []*expectedMsg{{
			errCode: codes.Internal,
		}},
	}, {
		desc: "unsupported default parameters",
		inServer: func() *Server {
			s := New()
			s.cs["testclient"] = &clientState{}
			return s
		}(),
		inCID: "testclient",
		wantMsg: []*expectedMsg{{
			errCode:   codes.Unimplemented,
			errReason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		}},
	}, {
		desc: "not expecting election ID",
		inServer: func() *Server {
			s := New()
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					ExpectElecID: false,
				},
			}
			return s
		}(),
		inCID: "testclient",
		wantMsg: []*expectedMsg{{
			errCode:   codes.Unimplemented,
			errReason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		}},
	}, {
		desc: "not expecting persist=false",
		inServer: func() *Server {
			s := New()
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist: false,
				},
			}
			return s
		}(),
		inCID: "testclient",
		wantMsg: []*expectedMsg{{
			errCode:   codes.Unimplemented,
			errReason: spb.ModifyRPCErrorDetails_UNSUPPORTED_PARAMS,
		}},
	}, {
		desc: "add to default network instance",
		inServer: func() *Server {
			s := New()
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              1,
			NetworkInstance: "",
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     1,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}},
	}, {
		desc: "add to unknown network instance",
		inServer: func() *Server {
			s := New()
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              42,
			NetworkInstance: "FISH",
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "1.1.1.1/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     42,
					Status: spb.AFTResult_FAILED,
				}},
			},
		}},
	}, {
		desc: "invalid operation",
		inServer: func() *Server {
			s := New()
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              84,
			NetworkInstance: "",
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "F-I-S-H",
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     84,
					Status: spb.AFTResult_FAILED,
				}},
			},
		}},
	}, {
		desc: "two valid operations",
		inServer: func() *Server {
			s := New()
			s.cs["testclient"] = &clientState{
				params: &clientParams{
					Persist:      true,
					ExpectElecID: true,
					FIBAck:       true,
				},
				lastElecID: &spb.Uint128{High: 42, Low: 42},
			}
			s.curElecID = &spb.Uint128{High: 42, Low: 42}
			s.curMaster = "testclient"
			return s
		}(),
		inCID: "testclient",
		inOps: []*spb.AFTOperation{{
			Id:              1,
			NetworkInstance: "",
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   1,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}, {
			Id:              2,
			NetworkInstance: "",
			Op:              spb.AFTOperation_ADD,
			ElectionId:      &spb.Uint128{High: 42, Low: 42},
			Entry: &spb.AFTOperation_NextHop{
				NextHop: &aftpb.Afts_NextHopKey{
					Index:   2,
					NextHop: &aftpb.Afts_NextHop{},
				},
			},
		}},
		wantMsg: []*expectedMsg{{
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     1,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}, {
			result: &spb.ModifyResponse{
				Result: []*spb.AFTResult{{
					Id:     2,
					Status: spb.AFTResult_FIB_PROGRAMMED,
				}},
			},
		}},
	}}

	type recvMsg struct {
		result *spb.ModifyResponse
		err    error
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			errCh := make(chan error)
			resCh := make(chan *spb.ModifyResponse)

			go tt.inServer.doModify(tt.inCID, tt.inOps, resCh, errCh)
			got := []*recvMsg{}
			for i := 0; i < len(tt.wantMsg); i++ {
				gotMsg := &recvMsg{}
				select {
				case err := <-errCh:
					gotMsg.err = err
				case got := <-resCh:
					gotMsg.result = got
				}
				got = append(got, gotMsg)
			}

			lessFn := func(i, j int) bool {
				switch {
				case got[i].err != nil && got[j].err != nil:
					return fmt.Sprintf("%v", got[i].err) < fmt.Sprintf("%v", got[j].err)
				case got[i].err != nil:
					return true
				case got[j].err != nil:
					return false
				default:
					iid := got[i].result.GetResult()[0].GetId()
					jid := got[j].result.GetResult()[0].GetId()
					return iid < jid
				}
			}
			sort.Slice(got, lessFn)
			sort.Slice(tt.wantMsg, lessFn)

			for i := 0; i < len(tt.wantMsg); i++ {
				wantMsg := tt.wantMsg[i]
				gotMsg := got[i]
				if err := gotMsg.err; err != nil {
					checkStatusErr(t, err, wantMsg.errCode, wantMsg.errReason)
				}
				if wantMsg.result != nil {
					if diff := cmp.Diff(gotMsg.result, wantMsg.result, protocmp.Transform()); diff != "" {
						t.Fatalf("did not get expected response, diff(-got,+want):\n%s", diff)
					}
				}
			}
		})
	}
}

func TestAddEntry(t *testing.T) {
	defName := DefaultNetworkInstanceName
	tests := []struct {
		desc           string
		inRIB          *rib.RIB
		inNI           string
		inOp           *spb.AFTOperation
		inFIBACK       bool
		inElection     *electionDetails
		wantResponse   *spb.ModifyResponse
		wantErrCode    codes.Code
		wantErrDetails spb.ModifyRPCErrorDetails_Reason
	}{{
		desc:        "nil election ID",
		inRIB:       rib.New(defName),
		inOp:        &spb.AFTOperation{},
		wantErrCode: codes.FailedPrecondition,
	}, {
		desc:  "invalid election",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 0, Low: 1},
		},
		wantErrCode: codes.Internal,
	}, {
		desc:  "client hasn't sent election ID",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 0, Low: 1},
		},
		inElection: &electionDetails{
			master: "some-client",
			ID:     &spb.Uint128{High: 1, Low: 1},
		},
		wantErrCode: codes.FailedPrecondition,
	}, {
		desc:  "client gives higher ID than known master",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 2, Low: 0},
			Id:         1,
		},
		inElection: &electionDetails{
			master: "this-client",
			// note: this is an internal error that should never happen :-)
			// so of course we test what happens when it does. In this case
			// we have got to the stage whereby we decided that our client sent
			// us a later ID than the one that we think is the master, even though
			// this client is the master. In this case, we've missed updating
			// the master ID somehow as new elections happen.
			ID:           &spb.Uint128{High: 1, Low: 1},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 2, Low: 0},
		},
		wantErrCode: codes.FailedPrecondition,
	}, {
		desc:  "client gives lower ID than known master",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 1, Low: 0},
			Id:         1,
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 1, Low: 1},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 1, Low: 0},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     1,
				Status: spb.AFTResult_FAILED,
			}},
		},
	}, {
		desc:  "client is not master - by name",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 0, Low: 1},
			Id:         2,
		},
		inElection: &electionDetails{
			master:       "not-this-client",
			ID:           &spb.Uint128{High: 1, Low: 1},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 2, Low: 0},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_FAILED,
			}},
		},
	}, {
		desc:  "client is not master - by mismatched latest",
		inRIB: rib.New(defName),
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 0, Low: 2},
			Id:         2,
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 0, Low: 1},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 0, Low: 1},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_FAILED,
			}},
		},
	}, {
		desc:        "nil operation",
		inRIB:       rib.New(defName),
		wantErrCode: codes.Internal,
	}, {
		desc:        "nil RIB",
		wantErrCode: codes.Internal,
	}, {
		desc:  "ADD v4: rib ACK",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "2.2.2.2/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}},
		},
	}, {
		desc:  "ADD v4: fib ACK",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Entry: &spb.AFTOperation_Ipv4{
				Ipv4: &aftpb.Afts_Ipv4EntryKey{
					Prefix: "2.2.2.2/32",
					Ipv4Entry: &aftpb.Afts_Ipv4Entry{
						NextHopGroup: &wpb.UintValue{Value: 1},
					},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		inFIBACK: true,
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_FIB_PROGRAMMED,
			}},
		},
	}, {
		desc:  "ADD NHG: unimplemented",
		inRIB: rib.New(defName, rib.DisableRIBCheckFn()),
		inNI:  defName,
		inOp: &spb.AFTOperation{
			ElectionId: &spb.Uint128{High: 4, Low: 2},
			Id:         2,
			Entry: &spb.AFTOperation_NextHopGroup{
				NextHopGroup: &aftpb.Afts_NextHopGroupKey{
					Id:           2,
					NextHopGroup: &aftpb.Afts_NextHopGroup{},
				},
			},
		},
		inElection: &electionDetails{
			master:       "this-client",
			ID:           &spb.Uint128{High: 4, Low: 2},
			client:       "this-client",
			clientLatest: &spb.Uint128{High: 4, Low: 2},
		},
		wantResponse: &spb.ModifyResponse{
			Result: []*spb.AFTResult{{
				Id:     2,
				Status: spb.AFTResult_RIB_PROGRAMMED,
			}},
		},
	}, {
		desc:        "nil RIB",
		inRIB:       nil,
		wantErrCode: codes.Internal,
	}, {
		desc:        "invalid RIB",
		inRIB:       &rib.RIB{},
		inNI:        "fish",
		wantErrCode: codes.Internal,
	}}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := addEntry(tt.inRIB, tt.inNI, tt.inOp, tt.inFIBACK, tt.inElection)
			if err != nil {
				checkStatusErr(t, err, tt.wantErrCode, tt.wantErrDetails)
			}
			if diff := cmp.Diff(got, tt.wantResponse, protocmp.Transform()); diff != "" {
				t.Fatalf("did not get expected response, diff(-got,+want):\n%s", diff)
			}
		})
	}
}
