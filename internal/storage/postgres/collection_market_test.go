package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/resource"
)

func TestSummaryPageCommitValidatesPublicInputBeforeDatabaseAccess(t *testing.T) {
	base := SummaryPageCommit{
		TargetID:        1,
		TaskID:          1,
		CombinationID:   1,
		ClaimGeneration: 1,
		ExpectedSwitch:  1,
		CursorBefore:    mustSummaryPageCursor(t, nil),
		CursorAfter:     mustSummaryPageCursor(t, []byte("next")),
		CollectedAt:     time.Date(2026, 8, 11, 12, 0, 0, 123456000, time.UTC),
	}
	tests := []struct {
		name   string
		mutate func(*SummaryPageCommit)
	}{
		{name: "zero target id", mutate: func(input *SummaryPageCommit) { input.TargetID = 0 }},
		{name: "negative target id", mutate: func(input *SummaryPageCommit) { input.TargetID = -1 }},
		{name: "zero task id", mutate: func(input *SummaryPageCommit) { input.TaskID = 0 }},
		{name: "negative task id", mutate: func(input *SummaryPageCommit) { input.TaskID = -1 }},
		{name: "zero combination id", mutate: func(input *SummaryPageCommit) { input.CombinationID = 0 }},
		{name: "negative combination id", mutate: func(input *SummaryPageCommit) { input.CombinationID = -1 }},
		{name: "zero claim generation", mutate: func(input *SummaryPageCommit) { input.ClaimGeneration = 0 }},
		{name: "negative claim generation", mutate: func(input *SummaryPageCommit) { input.ClaimGeneration = -1 }},
		{name: "zero expected switch", mutate: func(input *SummaryPageCommit) { input.ExpectedSwitch = 0 }},
		{name: "negative expected switch", mutate: func(input *SummaryPageCommit) { input.ExpectedSwitch = -1 }},
		{name: "negative ask total", mutate: func(input *SummaryPageCommit) { input.AskTotal = -1 }},
		{name: "zero collected time", mutate: func(input *SummaryPageCommit) { input.CollectedAt = time.Time{} }},
		{name: "year before postgres range", mutate: func(input *SummaryPageCommit) {
			input.CollectedAt = time.Date(0, 12, 31, 23, 59, 59, 0, time.UTC)
		}},
		{name: "year after postgres range", mutate: func(input *SummaryPageCommit) {
			input.CollectedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		}},
		{name: "non UTC location", mutate: func(input *SummaryPageCommit) {
			input.CollectedAt = time.Date(2026, 8, 11, 12, 0, 0, 0, time.FixedZone("zero-offset", 0))
		}},
		{name: "sub-microsecond precision", mutate: func(input *SummaryPageCommit) {
			input.CollectedAt = input.CollectedAt.Add(time.Nanosecond)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := base
			tt.mutate(&input)
			err, connections := commitSummaryPageWithRejectedDatabase(t, input)
			if !errors.Is(err, ErrCollectionInvalidInput) {
				t.Fatalf("CommitSummaryPage() error = %v, want %v", err, ErrCollectionInvalidInput)
			}
			if connections != 0 {
				t.Fatalf("database connections = %d, want 0", connections)
			}
		})
	}

	maximumCursor := mustSummaryPageCursor(t, make([]byte, collection.MaxCursorBytes))
	accepted := []struct {
		name  string
		input SummaryPageCommit
	}{
		{
			name: "minimum identity and timestamp",
			input: SummaryPageCommit{
				TargetID:        1,
				TaskID:          1,
				CombinationID:   1,
				ClaimGeneration: 1,
				ExpectedSwitch:  1,
				CursorBefore:    mustSummaryPageCursor(t, nil),
				CursorAfter:     mustSummaryPageCursor(t, nil),
				CollectedAt:     time.Date(1, 1, 1, 0, 0, 0, int(time.Microsecond), time.UTC),
			},
		},
		{
			name: "maximum identity cursor and timestamp",
			input: SummaryPageCommit{
				TargetID:        collection.TargetID(1<<63 - 1),
				TaskID:          collection.TaskID(1<<63 - 1),
				CombinationID:   resource.CombinationID(1<<63 - 1),
				ClaimGeneration: 1<<63 - 1,
				ExpectedSwitch:  collection.Revision(1<<63 - 1),
				CursorBefore:    maximumCursor,
				CursorAfter:     maximumCursor,
				CollectedAt:     time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC),
			},
		},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			err, connections := commitSummaryPageWithRejectedDatabase(t, tt.input)
			if !errors.Is(err, ErrCollectionStorage) {
				t.Fatalf("CommitSummaryPage() error = %v, want database boundary error %v", err, ErrCollectionStorage)
			}
			if connections == 0 {
				t.Fatal("valid public input did not reach the database boundary")
			}
		})
	}

	if _, err := collection.NewCursor(make([]byte, collection.MaxCursorBytes+1)); err == nil {
		t.Fatalf("NewCursor() accepted %d bytes", collection.MaxCursorBytes+1)
	}
}

func TestSummaryPageDigestCanonicalizesAttemptOrder(t *testing.T) {
	collectedAt := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	price := market.CNYCents(1234)
	orders := int64(2)
	items := int64(3)
	present, err := market.NewPresentObservation(market.PresentInput{
		Currency:    market.CurrencyCNY,
		Side:        market.SideAsk,
		PriceCents:  &price,
		OrderCount:  &orders,
		ItemCount:   &items,
		CollectedAt: collectedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := market.Observation{
		Side:        market.SideAsk,
		Status:      market.StatusFailed,
		CollectedAt: collectedAt,
	}
	firstAttempts := []AttemptWrite{
		{ProductID: 9, Observation: present},
		{ProductID: 3, Observation: failed, ReasonCode: "http.timeout"},
	}
	secondAttempts := []AttemptWrite{firstAttempts[1], firstAttempts[0]}
	firstBatch := observationBatch{
		AppID:    730,
		Platform: "steam",
		Side:     market.SideAsk,
		Order:    market.WriteOrder{SwitchVersion: 2, WriteSequence: 6},
		Attempts: firstAttempts,
	}
	secondBatch := firstBatch
	secondBatch.Attempts = secondAttempts
	firstSnapshots, err := prepareBatch(firstBatch, true)
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshots, err := prepareBatch(secondBatch, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstSnapshots) != 2 || firstSnapshots[0].productID != catalog.ProductID(3) || firstSnapshots[1].productID != catalog.ProductID(9) {
		t.Fatalf("snapshots were not canonically ordered: %#v", firstSnapshots)
	}
	input := SummaryPageCommit{
		TargetID:       5,
		TaskID:         7,
		ExpectedSwitch: 2,
		CursorBefore:   mustSummaryPageCursor(t, []byte("before")),
		CursorAfter:    mustSummaryPageCursor(t, []byte("after")),
		CollectedAt:    collectedAt,
		Attempts:       firstAttempts,
	}
	const writeSeq int64 = 6
	firstDigest := summaryPageDigest(input, writeSeq, firstBatch, firstSnapshots)
	input.Attempts = secondAttempts
	secondDigest := summaryPageDigest(input, writeSeq, secondBatch, secondSnapshots)
	if firstDigest != secondDigest {
		t.Fatalf("attempt order changed canonical digest: %x != %x", firstDigest, secondDigest)
	}
}

func TestSummaryPageDigestIncludesEverySemanticField(t *testing.T) {
	base := newSummaryDigestFixture(t)
	baseDigest := summaryPageDigest(base.input, base.writeSeq, base.batch, base.snapshots)
	const wantBaseDigest = "bf0b50657d1b42bc144dcd9c958c63d59df5811eb52eca8dcc07127c1ac292f8"
	if got := fmt.Sprintf("%x", baseDigest); got != wantBaseDigest {
		t.Fatalf("summary digest = %s, want canonical v2 digest %s", got, wantBaseDigest)
	}
	clone := base.clone()
	if got := summaryPageDigest(clone.input, clone.writeSeq, clone.batch, clone.snapshots); got != baseDigest {
		t.Fatalf("cloning fixture changed digest: %x != %x", got, baseDigest)
	}
	tests := []struct {
		name   string
		mutate func(*summaryDigestFixture)
	}{
		{name: "target id", mutate: func(fixture *summaryDigestFixture) { fixture.input.TargetID++ }},
		{name: "task id", mutate: func(fixture *summaryDigestFixture) { fixture.input.TaskID++ }},
		{name: "write seq", mutate: func(fixture *summaryDigestFixture) { fixture.writeSeq++ }},
		{name: "cursor before", mutate: func(fixture *summaryDigestFixture) {
			fixture.input.CursorBefore = mustSummaryPageCursor(t, []byte("different-before"))
		}},
		{name: "cursor after", mutate: func(fixture *summaryDigestFixture) {
			fixture.input.CursorAfter = mustSummaryPageCursor(t, []byte("different-after"))
		}},
		{name: "page collected time", mutate: func(fixture *summaryDigestFixture) {
			fixture.input.CollectedAt = fixture.input.CollectedAt.Add(time.Microsecond)
		}},
		{name: "appid", mutate: func(fixture *summaryDigestFixture) { fixture.batch.AppID++ }},
		{name: "platform", mutate: func(fixture *summaryDigestFixture) { fixture.batch.Platform = "buff" }},
		{name: "side", mutate: func(fixture *summaryDigestFixture) { fixture.batch.Side = market.SideBid }},
		{name: "switch version", mutate: func(fixture *summaryDigestFixture) { fixture.batch.Order.SwitchVersion++ }},
		{name: "write sequence", mutate: func(fixture *summaryDigestFixture) { fixture.batch.Order.WriteSequence++ }},
		{name: "attempt count", mutate: func(fixture *summaryDigestFixture) {
			fixture.snapshots = append(fixture.snapshots, attemptSnapshot{
				productID:   30,
				status:      market.StatusEmpty,
				collectedAt: fixture.input.CollectedAt,
			})
		}},
		{name: "product id", mutate: func(fixture *summaryDigestFixture) { fixture.snapshots[1].productID++ }},
		{name: "platform item id", mutate: func(fixture *summaryDigestFixture) {
			fixture.snapshots[0].platformItemID = "gid://steam/item/1"
		}},
		{name: "exact name", mutate: func(fixture *summaryDigestFixture) {
			fixture.snapshots[0].exactName = "AK-47 | Redline"
		}},
		{name: "status", mutate: func(fixture *summaryDigestFixture) {
			fixture.snapshots[1].status = market.StatusUnavailable
		}},
		{name: "source time presence", mutate: func(fixture *summaryDigestFixture) {
			fixture.snapshots[0].sourceTime = nil
		}},
		{name: "source time value", mutate: func(fixture *summaryDigestFixture) {
			changed := fixture.snapshots[0].sourceTime.Add(time.Microsecond)
			fixture.snapshots[0].sourceTime = &changed
		}},
		{name: "attempt collected time", mutate: func(fixture *summaryDigestFixture) {
			fixture.snapshots[0].collectedAt = fixture.snapshots[0].collectedAt.Add(time.Microsecond)
		}},
		{name: "reason code", mutate: func(fixture *summaryDigestFixture) {
			fixture.snapshots[1].reasonCode = "http.reset"
		}},
		{name: "present payload presence", mutate: func(fixture *summaryDigestFixture) {
			fixture.snapshots[0].present = nil
		}},
		{name: "price", mutate: func(fixture *summaryDigestFixture) {
			fixture.snapshots[0].present.priceCNYCents++
		}},
		{name: "order count presence", mutate: func(fixture *summaryDigestFixture) {
			fixture.snapshots[0].present.orderCount = nil
		}},
		{name: "order count value", mutate: func(fixture *summaryDigestFixture) {
			*fixture.snapshots[0].present.orderCount++
		}},
		{name: "item count presence", mutate: func(fixture *summaryDigestFixture) {
			fixture.snapshots[0].present.itemCount = nil
		}},
		{name: "item count value", mutate: func(fixture *summaryDigestFixture) {
			*fixture.snapshots[0].present.itemCount++
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := base.clone()
			tt.mutate(&fixture)
			got := summaryPageDigest(fixture.input, fixture.writeSeq, fixture.batch, fixture.snapshots)
			if got == baseDigest {
				t.Fatalf("changing %s did not change digest %x", tt.name, got)
			}
		})
	}
}

func TestSummaryPageDigestEmptyPageIsStable(t *testing.T) {
	fixture := newSummaryDigestFixture(t)
	fixture.input.Attempts = nil
	fixture.batch.Attempts = nil
	nilSnapshots, err := prepareBatch(fixture.batch, true)
	if err != nil {
		t.Fatal(err)
	}
	first := summaryPageDigest(fixture.input, fixture.writeSeq, fixture.batch, nilSnapshots)
	second := summaryPageDigest(fixture.input, fixture.writeSeq, fixture.batch, nilSnapshots)
	if first != second {
		t.Fatalf("repeated empty page digest changed: %x != %x", first, second)
	}
	if first == ([32]byte{}) {
		t.Fatal("empty page produced missing digest sentinel")
	}

	fixture.input.Attempts = []AttemptWrite{}
	fixture.batch.Attempts = []AttemptWrite{}
	emptySnapshots, err := prepareBatch(fixture.batch, true)
	if err != nil {
		t.Fatal(err)
	}
	explicitEmpty := summaryPageDigest(fixture.input, fixture.writeSeq, fixture.batch, emptySnapshots)
	if first != explicitEmpty {
		t.Fatalf("nil and explicit empty pages differ: %x != %x", first, explicitEmpty)
	}
}

func TestMapCollectionMarketWriteError(t *testing.T) {
	const privateDetail = "sensitive-database-detail"
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, deadlineCancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer deadlineCancel()
	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want error
	}{
		{name: "product not found", ctx: context.Background(), err: ErrProductNotFound, want: ErrCollectionInvalidInput},
		{name: "wrapped product not found", ctx: context.Background(), err: fmt.Errorf("%s: %w", privateDetail, ErrProductNotFound), want: ErrCollectionInvalidInput},
		{name: "product scope mismatch", ctx: context.Background(), err: fmt.Errorf("%s: %w", privateDetail, errProductScopeMismatch), want: ErrCollectionInvalidInput},
		{name: "stale observation", ctx: context.Background(), err: fmt.Errorf("%s: %w", privateDetail, ErrStaleObservation), want: ErrCollectionPageConflict},
		{name: "observation conflict", ctx: context.Background(), err: fmt.Errorf("%s: %w", privateDetail, ErrObservationConflict), want: ErrCollectionPageConflict},
		{name: "market integrity", ctx: context.Background(), err: fmt.Errorf("%s: %w", privateDetail, ErrMarketIntegrity), want: ErrCollectionIntegrity},
		{name: "unknown write failure", ctx: context.Background(), err: errors.New(privateDetail), want: ErrCollectionStorage},
		{name: "cancelled context", ctx: cancelled, err: errors.New(privateDetail), want: context.Canceled},
		{name: "expired context", ctx: deadline, err: errors.New(privateDetail), want: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapCollectionMarketWriteError(tt.ctx, tt.err)
			if got != tt.want {
				t.Fatalf("mapCollectionMarketWriteError() = %v, want %v", got, tt.want)
			}
			if strings.Contains(got.Error(), privateDetail) {
				t.Fatalf("mapCollectionMarketWriteError() leaked private detail: %v", got)
			}
		})
	}
}

func TestSummaryPageCommitExposesNoScopeOrderOrDigest(t *testing.T) {
	typeOf := reflect.TypeOf(SummaryPageCommit{})
	want := []struct {
		name   string
		typeOf reflect.Type
	}{
		{name: "TargetID", typeOf: reflect.TypeOf(collection.TargetID(0))},
		{name: "TaskID", typeOf: reflect.TypeOf(collection.TaskID(0))},
		{name: "CombinationID", typeOf: reflect.TypeOf(resource.CombinationID(0))},
		{name: "ClaimGeneration", typeOf: reflect.TypeOf(int64(0))},
		{name: "ExpectedSwitch", typeOf: reflect.TypeOf(collection.Revision(0))},
		{name: "CursorBefore", typeOf: reflect.TypeOf(collection.Cursor{})},
		{name: "CursorAfter", typeOf: reflect.TypeOf(collection.Cursor{})},
		{name: "CollectedAt", typeOf: reflect.TypeOf(time.Time{})},
		{name: "Attempts", typeOf: reflect.TypeOf([]AttemptWrite(nil))},
		// Payload 是平台原始响应，只作展示副本。它既不是作用域也不是因果顺序，
		// 更不是页面摘要（摘要仍由存储层从 attempts 派生，不接受调用方提交）。
		{name: "Payload", typeOf: reflect.TypeOf([]byte(nil))},
		// 执行身份只有持有租约的调度器知道，存储层无法从目标派生，因此必须由调用方提交。
		{name: "AccountID", typeOf: reflect.TypeOf(int64(0))},
		{name: "ExitAddress", typeOf: reflect.TypeOf(netip.Addr{})},
		{name: "AskTotal", typeOf: reflect.TypeOf(int64(0))},
	}
	if typeOf.NumField() != len(want) {
		t.Fatalf("SummaryPageCommit has %d fields, want exactly %d page facts", typeOf.NumField(), len(want))
	}
	for index, expected := range want {
		field := typeOf.Field(index)
		if field.Name != expected.name || field.Type != expected.typeOf {
			t.Fatalf("field %d = %s %v, want %s %v", index, field.Name, field.Type, expected.name, expected.typeOf)
		}
		if field.Anonymous || field.PkgPath != "" {
			t.Fatalf("field %s must be an explicit exported page fact", field.Name)
		}
		lowerName := strings.ToLower(field.Name)
		for _, forbidden := range []string{"scope", "platform", "appid", "side", "order", "switchversion", "runsequence", "digest"} {
			if strings.Contains(lowerName, forbidden) {
				t.Fatalf("caller-controlled field %q exposes forbidden %s data", field.Name, forbidden)
			}
		}
		if field.Type == reflect.TypeOf(market.WriteOrder{}) || field.Type == reflect.TypeOf([32]byte{}) {
			t.Fatalf("caller-controlled field %q exposes order or digest data", field.Name)
		}
	}

	attemptType := reflect.TypeOf(AttemptWrite{})
	wantAttempt := []struct {
		name   string
		typeOf reflect.Type
	}{
		{name: "ProductID", typeOf: reflect.TypeOf(catalog.ProductID(0))},
		{name: "PlatformItemID", typeOf: reflect.TypeOf("")},
		{name: "ExactName", typeOf: reflect.TypeOf("")},
		{name: "Observation", typeOf: reflect.TypeOf(market.Observation{})},
		{name: "ReasonCode", typeOf: reflect.TypeOf("")},
		// Media 只用于控制台呈现，不参与行情判定与页面摘要。
		{name: "Media", typeOf: reflect.TypeOf(catalog.ProductMedia{})},
	}
	if attemptType.NumField() != len(wantAttempt) {
		t.Fatalf("AttemptWrite has %d fields, want exactly %d attempt facts", attemptType.NumField(), len(wantAttempt))
	}
	for index, expected := range wantAttempt {
		field := attemptType.Field(index)
		if field.Name != expected.name || field.Type != expected.typeOf || field.Anonymous || field.PkgPath != "" {
			t.Fatalf("attempt field %d = %s %v, want explicit exported %s %v", index, field.Name, field.Type, expected.name, expected.typeOf)
		}
	}
}

type summaryDigestFixture struct {
	input     SummaryPageCommit
	writeSeq  int64
	batch     observationBatch
	snapshots []attemptSnapshot
}

func newSummaryDigestFixture(t *testing.T) summaryDigestFixture {
	t.Helper()
	sourceTime := time.Date(2026, 8, 11, 11, 58, 0, 123000000, time.UTC)
	collectedAt := time.Date(2026, 8, 11, 12, 0, 0, 456000000, time.UTC)
	orders := int64(2)
	items := int64(3)
	return summaryDigestFixture{
		input: SummaryPageCommit{
			TargetID:       5,
			TaskID:         7,
			ExpectedSwitch: 2,
			CursorBefore:   mustSummaryPageCursor(t, []byte("before")),
			CursorAfter:    mustSummaryPageCursor(t, []byte("after")),
			CollectedAt:    collectedAt,
		},
		writeSeq: 6,
		batch: observationBatch{
			AppID:    730,
			Platform: "steam",
			Side:     market.SideAsk,
			Order:    market.WriteOrder{SwitchVersion: 2, WriteSequence: 6},
		},
		snapshots: []attemptSnapshot{
			{
				productID:   10,
				status:      market.StatusPresent,
				sourceTime:  &sourceTime,
				collectedAt: collectedAt.Add(-time.Minute),
				present: &presentSnapshot{
					priceCNYCents: 1234,
					orderCount:    &orders,
					itemCount:     &items,
				},
			},
			{
				productID:   20,
				status:      market.StatusFailed,
				collectedAt: collectedAt.Add(-30 * time.Second),
				reasonCode:  "http.timeout",
			},
		},
	}
}

func (fixture summaryDigestFixture) clone() summaryDigestFixture {
	fixture.input.Attempts = append([]AttemptWrite(nil), fixture.input.Attempts...)
	fixture.batch.Attempts = append([]AttemptWrite(nil), fixture.batch.Attempts...)
	fixture.snapshots = append([]attemptSnapshot(nil), fixture.snapshots...)
	for index := range fixture.snapshots {
		snapshot := &fixture.snapshots[index]
		if snapshot.sourceTime != nil {
			copied := *snapshot.sourceTime
			snapshot.sourceTime = &copied
		}
		if snapshot.present != nil {
			copied := *snapshot.present
			if copied.orderCount != nil {
				count := *copied.orderCount
				copied.orderCount = &count
			}
			if copied.itemCount != nil {
				count := *copied.itemCount
				copied.itemCount = &count
			}
			snapshot.present = &copied
		}
	}
	return fixture
}

func mustSummaryPageCursor(t *testing.T, value []byte) collection.Cursor {
	t.Helper()
	cursor, err := collection.NewCursor(value)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

func commitSummaryPageWithRejectedDatabase(t *testing.T, input SummaryPageCommit) (error, int64) {
	t.Helper()
	connector := &summaryPageRejectingConnector{}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { _ = db.Close() })
	store := &Store{db: db}
	_, _, err := store.CommitSummaryPage(context.Background(), input)
	return err, connector.connections.Load()
}

type summaryPageRejectingConnector struct {
	connections atomic.Int64
}

func (connector *summaryPageRejectingConnector) Connect(context.Context) (driver.Conn, error) {
	connector.connections.Add(1)
	return nil, errors.New("unexpected summary page database access")
}

func (connector *summaryPageRejectingConnector) Driver() driver.Driver {
	return connector
}

func (*summaryPageRejectingConnector) Open(string) (driver.Conn, error) {
	return nil, errors.New("unexpected summary page database access")
}
