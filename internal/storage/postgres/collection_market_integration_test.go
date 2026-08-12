package postgres

import (
	"errors"
	"sync"
	"testing"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
	"buff-go/internal/market"
)

func testCollectionSummaryPages(t *testing.T, dsn string) {
	t.Run("atomic page and market lifecycle", func(t *testing.T) {
		store, db := migratedStore(t, dsn)
		ctx := t.Context()
		run, target, products := summaryPageFixture(t, store, 730, "page-lifecycle", 2)
		startedAt, _ := run.StartedAt()
		collectedAt := startedAt.Add(time.Second)
		price := market.CNYCents(12345)
		present, err := market.NewPresentObservation(market.PresentInput{
			Currency: market.CurrencyCNY, Side: market.SideAsk, PriceCents: &price, CollectedAt: collectedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		failed := market.Observation{Side: market.SideAsk, Status: market.StatusFailed, CollectedAt: collectedAt}
		next := mustCollectionCursor(t, []byte("next"))
		input := SummaryPageCommit{
			RunID: run.ID(), PageSequence: 1, CursorBefore: run.CurrentCursor(), CursorAfter: next,
			CollectedAt: collectedAt,
			Attempts: []AttemptWrite{
				{ProductID: products[0].ProductID, Observation: present},
				{ProductID: products[1].ProductID, Observation: failed, ReasonCode: "fetch.timeout"},
			},
		}
		page, applied, err := store.CommitSummaryPage(ctx, input)
		if err != nil || !applied || page.PageSequence() != 1 {
			t.Fatalf("CommitSummaryPage() page=%+v applied=%v err=%v", page, applied, err)
		}
		latest, found, err := store.LatestAttempt(ctx, MarketKey{ProductID: products[0].ProductID, Platform: "page-lifecycle", Side: market.SideAsk})
		if err != nil || !found || latest.Order != (market.WriteOrder{SwitchVersion: int64(target.SwitchVersion()), RunSequence: int64(run.RunSequence()), PageSequence: 1}) {
			t.Fatalf("latest = %+v found=%v err=%v", latest, found, err)
		}
		last, found, err := store.LastPresent(ctx, latest.Key)
		if err != nil || !found || last.Observation.Summary == nil || last.Observation.Summary.PriceCents != price {
			t.Fatalf("last present = %+v found=%v err=%v", last, found, err)
		}
		if retry, retryApplied, err := store.CommitSummaryPage(ctx, input); err != nil || retryApplied || retry.PayloadDigest() != page.PayloadDigest() {
			t.Fatalf("exact retry = %+v applied=%v err=%v", retry, retryApplied, err)
		}
		changed := input
		changed.Attempts = append([]AttemptWrite(nil), input.Attempts...)
		changed.Attempts[1].ReasonCode = "fetch.changed"
		if _, _, err := store.CommitSummaryPage(ctx, changed); !errors.Is(err, ErrCollectionPageConflict) {
			t.Fatalf("changed retry error = %v", err)
		}
		gap := SummaryPageCommit{RunID: run.ID(), PageSequence: 3, CursorBefore: next, CollectedAt: collectedAt.Add(time.Second)}
		if _, _, err := store.CommitSummaryPage(ctx, gap); !errors.Is(err, ErrCollectionPageOrder) {
			t.Fatalf("page gap error = %v", err)
		}
		wrongCursor := SummaryPageCommit{RunID: run.ID(), PageSequence: 2, CursorBefore: run.CurrentCursor(), CollectedAt: collectedAt.Add(time.Second)}
		if _, _, err := store.CommitSummaryPage(ctx, wrongCursor); !errors.Is(err, ErrCollectionPageOrder) {
			t.Fatalf("wrong cursor error = %v", err)
		}
		empty := SummaryPageCommit{
			RunID: run.ID(), PageSequence: 2, CursorBefore: next,
			CollectedAt: collectedAt.Add(time.Second),
		}
		if _, applied, err := store.CommitSummaryPage(ctx, empty); err != nil || !applied {
			t.Fatalf("empty page applied=%v err=%v", applied, err)
		}
		pages, err := store.Pages(ctx, run.ID())
		if err != nil || len(pages) != 2 || !pages[1].CursorBefore().Equal(next) {
			t.Fatalf("pages = %+v err=%v", pages, err)
		}
		storedRun, found, err := store.Run(ctx, run.ID())
		if err != nil || !found || storedRun.LastPageSequence() != 2 {
			t.Fatalf("stored run = %+v found=%v err=%v", storedRun, found, err)
		}
		storedRun, err = store.FinishRun(ctx, run.ID(), collection.RunSucceeded, collection.CompletenessComplete, collection.RunReasonNone)
		if err != nil || storedRun.State() != collection.RunSucceeded {
			t.Fatalf("finish run = %+v err=%v", storedRun, err)
		}
		if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID: run.ID(), PageSequence: 3, CollectedAt: collectedAt.Add(2 * time.Second),
		}); !errors.Is(err, ErrCollectionFence) {
			t.Fatalf("terminal page error = %v", err)
		}
		restarted, err := New(db)
		if err != nil {
			t.Fatal(err)
		}
		if restored, err := restarted.Pages(ctx, run.ID()); err != nil || len(restored) != 2 {
			t.Fatalf("restart pages = %+v err=%v", restored, err)
		}
	})

	t.Run("fence and transaction rollback", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		run, target, products := summaryPageFixture(t, store, 730, "page-fence", 1)
		wrongApp, err := store.CreateSteamProduct(ctx, 440, "wrong-app")
		if err != nil {
			t.Fatal(err)
		}
		startedAt, _ := run.StartedAt()
		collectedAt := startedAt.Add(time.Second)
		valid := market.Observation{Side: market.SideBid, Status: market.StatusEmpty, CollectedAt: collectedAt}
		invalidScope := SummaryPageCommit{
			RunID: run.ID(), PageSequence: 1, CollectedAt: collectedAt,
			Attempts: []AttemptWrite{
				{ProductID: products[0].ProductID, Observation: valid},
				{ProductID: wrongApp.ProductID, Observation: valid},
			},
		}
		if _, _, err := store.CommitSummaryPage(ctx, invalidScope); !errors.Is(err, ErrCollectionInvalidInput) {
			t.Fatalf("cross-app page error = %v", err)
		}
		assertNoCommittedSummaryPage(t, store, run, products[0].ProductID)

		oldAttempt := valid
		oldAttempt.CollectedAt = startedAt.Add(-time.Microsecond)
		if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID: run.ID(), PageSequence: 1, CollectedAt: collectedAt,
			Attempts: []AttemptWrite{{ProductID: products[0].ProductID, Observation: oldAttempt}},
		}); !errors.Is(err, ErrCollectionInvalidInput) {
			t.Fatalf("pre-run attempt error = %v", err)
		}
		assertNoCommittedSummaryPage(t, store, run, products[0].ProductID)

		target, err = store.SetTargetDesired(ctx, target.ID(), target.Revision(), collection.DesiredDisabled)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{RunID: run.ID(), PageSequence: 1, CollectedAt: collectedAt}); !errors.Is(err, ErrCollectionFence) {
			t.Fatalf("disabled page error = %v", err)
		}
		target, err = store.TransitionTarget(ctx, target.ID(), target.Revision(), target.SwitchVersion(), TargetTransition{State: collection.ActualStopped})
		if err != nil {
			t.Fatal(err)
		}
		target, err = store.SetTargetDesired(ctx, target.ID(), target.Revision(), collection.DesiredEnabled)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{RunID: run.ID(), PageSequence: 1, CollectedAt: collectedAt}); !errors.Is(err, ErrCollectionFence) {
			t.Fatalf("old-switch page error = %v", err)
		}
		assertNoCommittedSummaryPage(t, store, run, products[0].ProductID)
	})

	t.Run("concurrent retry and orphan facts", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		run, target, products := summaryPageFixture(t, store, 252490, "page-concurrent", 1)
		startedAt, _ := run.StartedAt()
		collectedAt := startedAt.Add(time.Second)
		observation := market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt}
		input := SummaryPageCommit{
			RunID: run.ID(), PageSequence: 1, CollectedAt: collectedAt,
			Attempts: []AttemptWrite{{ProductID: products[0].ProductID, Observation: observation}},
		}
		var wg sync.WaitGroup
		results := make(chan struct {
			applied bool
			err     error
		}, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, applied, err := store.CommitSummaryPage(ctx, input)
				results <- struct {
					applied bool
					err     error
				}{applied: applied, err: err}
			}()
		}
		wg.Wait()
		close(results)
		appliedCount := 0
		for result := range results {
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.applied {
				appliedCount++
			}
		}
		if appliedCount != 1 {
			t.Fatalf("concurrent applied count = %d", appliedCount)
		}

		secondRunTarget, err := store.CreateSummaryTarget(ctx, "page-orphan", market.SideAsk, collection.DesiredEnabled)
		if err != nil {
			t.Fatal(err)
		}
		secondRun, created, err := store.CreateSummaryRun(ctx, secondRunTarget.ID(), 252490, secondRunTarget.SwitchVersion(), mustCollectionCursor(t, nil))
		if err != nil || !created {
			t.Fatalf("orphan run = %+v created=%v err=%v", secondRun, created, err)
		}
		secondRun, err = store.BeginRun(ctx, secondRun.ID())
		if err != nil {
			t.Fatal(err)
		}
		secondStarted, _ := secondRun.StartedAt()
		secondCollected := secondStarted.Add(time.Second)
		orphanObservation := market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: secondCollected}
		order := market.WriteOrder{
			SwitchVersion: int64(secondRunTarget.SwitchVersion()), RunSequence: int64(secondRun.RunSequence()), PageSequence: 1,
		}
		if applied, err := store.saveObservations(ctx, observationBatch{
			AppID: 252490, Platform: "page-orphan", Side: market.SideAsk, Order: order,
			Attempts: []AttemptWrite{{ProductID: products[0].ProductID, Observation: orphanObservation}},
		}); err != nil || !applied {
			t.Fatalf("seed orphan market fact applied=%v err=%v", applied, err)
		}
		if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID: secondRun.ID(), PageSequence: 1, CollectedAt: secondCollected,
			Attempts: []AttemptWrite{{ProductID: products[0].ProductID, Observation: orphanObservation}},
		}); !errors.Is(err, ErrCollectionIntegrity) {
			t.Fatalf("orphan market fact error = %v", err)
		}
		pages, err := store.Pages(ctx, secondRun.ID())
		if err != nil || len(pages) != 0 {
			t.Fatalf("orphan page rollback = %+v err=%v", pages, err)
		}
		_ = target
	})
}

func testCollectionCatalogPages(t *testing.T, dsn string) {
	t.Run("catalog page lifecycle", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		run, _ := catalogPageFixture(t, store, 730, time.Hour)
		startedAt, _ := run.StartedAt()
		collectedAt := startedAt.Add(time.Second)
		next := mustCollectionCursor(t, []byte("catalog-next"))
		input := CatalogPageCommit{
			RunID: run.ID(), PageSequence: 1, CursorBefore: run.CurrentCursor(),
			CursorAfter: next, CollectedAt: collectedAt,
		}
		page, applied, err := store.CommitCatalogPage(ctx, input)
		if err != nil || !applied || page.PageSequence() != 1 {
			t.Fatalf("CommitCatalogPage() page=%+v applied=%v err=%v", page, applied, err)
		}
		if retry, retryApplied, err := store.CommitCatalogPage(ctx, input); err != nil || retryApplied || retry.PayloadDigest() != page.PayloadDigest() {
			t.Fatalf("exact retry = %+v applied=%v err=%v", retry, retryApplied, err)
		}
		changed := input
		changed.CollectedAt = collectedAt.Add(time.Second)
		if _, _, err := store.CommitCatalogPage(ctx, changed); !errors.Is(err, ErrCollectionPageConflict) {
			t.Fatalf("changed retry error = %v", err)
		}
		gap := CatalogPageCommit{RunID: run.ID(), PageSequence: 3, CursorBefore: next, CollectedAt: collectedAt.Add(time.Second)}
		if _, _, err := store.CommitCatalogPage(ctx, gap); !errors.Is(err, ErrCollectionPageOrder) {
			t.Fatalf("page gap error = %v", err)
		}
		wrongCursor := CatalogPageCommit{RunID: run.ID(), PageSequence: 2, CursorBefore: run.CurrentCursor(), CollectedAt: collectedAt.Add(time.Second)}
		if _, _, err := store.CommitCatalogPage(ctx, wrongCursor); !errors.Is(err, ErrCollectionPageOrder) {
			t.Fatalf("wrong cursor error = %v", err)
		}
		second := CatalogPageCommit{
			RunID: run.ID(), PageSequence: 2, CursorBefore: next, CollectedAt: collectedAt.Add(time.Second),
		}
		if _, applied, err := store.CommitCatalogPage(ctx, second); err != nil || !applied {
			t.Fatalf("second page applied=%v err=%v", applied, err)
		}
		storedRun, found, err := store.Run(ctx, run.ID())
		if err != nil || !found || storedRun.LastPageSequence() != 2 {
			t.Fatalf("stored run = %+v found=%v err=%v", storedRun, found, err)
		}
		if _, err := store.FinishRun(ctx, run.ID(), collection.RunSucceeded, collection.CompletenessComplete, collection.RunReasonNone); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.CommitCatalogPage(ctx, CatalogPageCommit{
			RunID: run.ID(), PageSequence: 3, CursorBefore: next, CollectedAt: collectedAt.Add(2 * time.Second),
		}); !errors.Is(err, ErrCollectionFence) {
			t.Fatalf("terminal page error = %v", err)
		}
	})

	t.Run("catalog page fences", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		run, target := catalogPageFixture(t, store, 440, time.Hour)
		startedAt, _ := run.StartedAt()
		collectedAt := startedAt.Add(time.Second)
		// 摘要提交入口拒绝目录运行。
		if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID: run.ID(), PageSequence: 1, CursorBefore: run.CurrentCursor(), CollectedAt: collectedAt,
		}); !errors.Is(err, ErrCollectionIntegrity) {
			t.Fatalf("summary commit for catalog run error = %v", err)
		}
		if _, err := store.SetTargetDesired(ctx, target.ID(), target.Revision(), collection.DesiredDisabled); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.CommitCatalogPage(ctx, CatalogPageCommit{
			RunID: run.ID(), PageSequence: 1, CursorBefore: run.CurrentCursor(), CollectedAt: collectedAt,
		}); !errors.Is(err, ErrCollectionFence) {
			t.Fatalf("disabled page error = %v", err)
		}
		if pages, err := store.Pages(ctx, run.ID()); err != nil || len(pages) != 0 {
			t.Fatalf("fenced pages = %+v err=%v", pages, err)
		}
	})
}

func catalogPageFixture(t *testing.T, store *Store, appID int64, period time.Duration) (collection.Run, collection.Target) {
	t.Helper()
	ctx := t.Context()
	target, err := store.CreateCatalogTarget(ctx, appID, period, collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := store.CreateCatalogRun(ctx, target.ID(), target.SwitchVersion(), mustCollectionCursor(t, nil))
	if err != nil || !created {
		t.Fatalf("CreateCatalogRun() run=%+v created=%v err=%v", run, created, err)
	}
	run, err = store.BeginRun(ctx, run.ID())
	if err != nil {
		t.Fatal(err)
	}
	return run, target
}

func summaryPageFixture(t *testing.T, store *Store, appID int64, platform string, productCount int) (collection.Run, collection.Target, []catalog.SteamProduct) {
	t.Helper()
	ctx := t.Context()
	products := make([]catalog.SteamProduct, 0, productCount)
	for index := range productCount {
		product, err := store.CreateSteamProduct(ctx, appID, platform+"-product-"+time.Unix(int64(index), 0).UTC().Format("150405"))
		if err != nil {
			t.Fatal(err)
		}
		products = append(products, product)
	}
	target, err := store.CreateSummaryTarget(ctx, collection.Platform(platform), pageFixtureSide(platform), collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := store.CreateSummaryRun(ctx, target.ID(), appID, target.SwitchVersion(), mustCollectionCursor(t, nil))
	if err != nil || !created {
		t.Fatalf("CreateSummaryRun() run=%+v created=%v err=%v", run, created, err)
	}
	run, err = store.BeginRun(ctx, run.ID())
	if err != nil {
		t.Fatal(err)
	}
	return run, target, products
}

func pageFixtureSide(platform string) market.Side {
	if platform == "page-fence" {
		return market.SideBid
	}
	return market.SideAsk
}

func assertNoCommittedSummaryPage(t *testing.T, store *Store, run collection.Run, productID catalog.ProductID) {
	t.Helper()
	pages, err := store.Pages(t.Context(), run.ID())
	if err != nil || len(pages) != 0 {
		t.Fatalf("unexpected pages = %+v err=%v", pages, err)
	}
	stored, found, err := store.Run(t.Context(), run.ID())
	if err != nil || !found || stored.LastPageSequence() != 0 || !stored.CurrentCursor().Equal(run.CurrentCursor()) {
		t.Fatalf("run changed = %+v found=%v err=%v", stored, found, err)
	}
	if _, found, err := store.LatestAttempt(t.Context(), MarketKey{ProductID: productID, Platform: string(run.Platform()), Side: mustRunSide(t, run)}); err != nil || found {
		t.Fatalf("market fact found=%v err=%v", found, err)
	}
}

func mustRunSide(t *testing.T, run collection.Run) market.Side {
	t.Helper()
	side, present := run.Side()
	if !present {
		t.Fatal("summary run has no side")
	}
	return side
}
