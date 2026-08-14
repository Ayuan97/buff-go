package postgres

import (
	"errors"
	"net/netip"
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

		secondRunTarget, err := store.CreateSummaryTarget(ctx, "page-orphan", 252490, market.SideAsk, collection.DesiredEnabled)
		if err != nil {
			t.Fatal(err)
		}
		secondRun, created, err := store.CreateSummaryRun(ctx, secondRunTarget.ID(), secondRunTarget.SwitchVersion(), mustCollectionCursor(t, nil))
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

	t.Run("exact name creates product", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		run, _, _ := summaryPageFixture(t, store, 730, "page-name", 0)
		startedAt, _ := run.StartedAt()
		collectedAt := startedAt.Add(time.Second)
		observation := market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt}
		page, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID: run.ID(), PageSequence: 1, CollectedAt: collectedAt,
			Attempts: []AttemptWrite{{ExactName: "AK-47 | Redline", Observation: observation}},
		})
		if err != nil || !applied {
			t.Fatalf("CommitSummaryPage() page=%+v applied=%v err=%v", page, applied, err)
		}
		products, err := store.ListSteamProductsByAppID(ctx, 730)
		if err != nil || len(products) != 1 || products[0].Name != "AK-47 | Redline" {
			t.Fatalf("created products = %+v err=%v", products, err)
		}
		latest, found, err := store.LatestAttempt(ctx, MarketKey{ProductID: products[0].ProductID, Platform: "page-name", Side: market.SideAsk})
		if err != nil || !found || latest.Status != market.StatusEmpty {
			t.Fatalf("latest = %+v found=%v err=%v", latest, found, err)
		}
		retry, retryApplied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID: run.ID(), PageSequence: 1, CollectedAt: collectedAt,
			Attempts: []AttemptWrite{{ExactName: "AK-47 | Redline", Observation: observation}},
		})
		if err != nil || retryApplied || retry.PayloadDigest() != page.PayloadDigest() {
			t.Fatalf("exact name retry = %+v applied=%v err=%v", retry, retryApplied, err)
		}
	})

	// 展示元数据必须能补到早先建好的商品上，否则老商品永远没有图标。
	t.Run("product media backfill and attribution", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		run, _, _ := summaryPageFixture(t, store, 730, "page-media", 0)
		startedAt, _ := run.StartedAt()
		collectedAt := startedAt.Add(time.Second)
		observation := market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt}

		// 第一页不带元数据，模拟本次迁移之前建立的商品
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID: run.ID(), PageSequence: 1, CollectedAt: collectedAt,
			Attempts: []AttemptWrite{{ExactName: "AK-47 | Redline", Observation: observation}},
		}); err != nil || !applied {
			t.Fatalf("commit without media applied=%v err=%v", applied, err)
		}
		bare, err := store.ListMarketQuotes(ctx, MarketQuoteFilter{AppID: 730, Platform: "page-media", Limit: 10})
		if err != nil || len(bare.Quotes) != 1 || !bare.Quotes[0].Media.Empty() {
			t.Fatalf("quotes before backfill = %+v err=%v", bare.Quotes, err)
		}

		// 第二页带上元数据，同一个商品应被补齐而不是新建
		next := mustCollectionCursor(t, []byte("2"))
		media := catalog.ProductMedia{IconPath: "iconAK47", ItemType: "Rifle", NameColor: "d2d2d2"}
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID: run.ID(), PageSequence: 2, CursorBefore: mustCollectionCursor(t, nil), CursorAfter: next,
			CollectedAt: collectedAt.Add(time.Second),
			Attempts: []AttemptWrite{{
				ExactName:   "AK-47 | Redline",
				Observation: market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt.Add(time.Second)},
				Media:       media,
			}},
			AccountID:   77,
			ExitAddress: netip.MustParseAddr("38.175.103.188"),
		}); err != nil || !applied {
			t.Fatalf("commit with media applied=%v err=%v", applied, err)
		}
		filled, err := store.ListMarketQuotes(ctx, MarketQuoteFilter{AppID: 730, Platform: "page-media", Limit: 10})
		if err != nil || len(filled.Quotes) != 1 || filled.Quotes[0].Media != media {
			t.Fatalf("quotes after backfill = %+v err=%v", filled.Quotes, err)
		}

		// 归属只记在写它的那一页上，未记录的页保持为空
		summaries, err := store.PageSummaries(ctx, run.ID())
		if err != nil || len(summaries) != 2 {
			t.Fatalf("summaries = %+v err=%v", summaries, err)
		}
		if summaries[0].AccountID != 0 || summaries[0].ExitAddress != "" {
			t.Fatalf("first page attribution = %+v", summaries[0])
		}
		if summaries[1].AccountID != 77 || summaries[1].ExitAddress != "38.175.103.188" {
			t.Fatalf("second page attribution = %+v", summaries[1])
		}

		// 非法元数据必须降级为空，不能让整页提交失败
		third := mustCollectionCursor(t, []byte("3"))
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID: run.ID(), PageSequence: 3, CursorBefore: next, CursorAfter: third,
			CollectedAt: collectedAt.Add(2 * time.Second),
			Attempts: []AttemptWrite{{
				ExactName:   "AWP | Asiimov",
				Observation: market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt.Add(2 * time.Second)},
				Media:       catalog.ProductMedia{IconPath: "../../etc/passwd", NameColor: "not-hex"},
			}},
		}); err != nil || !applied {
			t.Fatalf("commit with invalid media applied=%v err=%v", applied, err)
		}
		unsafe, err := store.ListMarketQuotes(ctx, MarketQuoteFilter{
			AppID: 730, Platform: "page-media", Keyword: "Asiimov", Limit: 10,
		})
		if err != nil || len(unsafe.Quotes) != 1 || !unsafe.Quotes[0].Media.Empty() {
			t.Fatalf("invalid media was stored: %+v err=%v", unsafe.Quotes, err)
		}
	})

	t.Run("page payload round trip", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		run, target, products := summaryPageFixture(t, store, 730, "page-payload", 1)
		startedAt, _ := run.StartedAt()
		collectedAt := startedAt.Add(time.Second)
		observation := market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt}
		raw := []byte(`{"success":true,"total_count":35252,"results":[]}`)
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID: run.ID(), PageSequence: 1, CollectedAt: collectedAt,
			Attempts: []AttemptWrite{{ProductID: products[0].ProductID, Observation: observation}},
			Payload:  raw,
		}); err != nil || !applied {
			t.Fatalf("commit with payload applied=%v err=%v", applied, err)
		}
		stored, err := store.PagePayload(ctx, run.ID(), 1)
		if err != nil || string(stored) != string(raw) {
			t.Fatalf("payload = %q err=%v", stored, err)
		}
		summaries, err := store.PageSummaries(ctx, run.ID())
		if err != nil || len(summaries) != 1 || summaries[0].PayloadBytes != int64(len(raw)) {
			t.Fatalf("summaries = %+v err=%v", summaries, err)
		}
		attempts, err := store.PageAttempts(ctx, run.ID(), 1)
		if err != nil || len(attempts) != 1 || attempts[0].ProductID != int64(products[0].ProductID) {
			t.Fatalf("attempts = %+v err=%v", attempts, err)
		}
		// 目标的开关版本参与定位，换版本重采后旧页就不再拥有这条事实
		if int64(target.SwitchVersion()) < 1 {
			t.Fatalf("switch version = %d", target.SwitchVersion())
		}

		// 没有 payload 的方向提交后，页在但副本不在
		next := mustCollectionCursor(t, []byte("2"))
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			RunID: run.ID(), PageSequence: 2, CursorBefore: mustCollectionCursor(t, nil), CursorAfter: next,
			CollectedAt: collectedAt.Add(time.Second),
			Attempts:    []AttemptWrite{{ProductID: products[0].ProductID, Observation: market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt.Add(time.Second)}}},
		}); err != nil || !applied {
			t.Fatalf("commit without payload applied=%v err=%v", applied, err)
		}
		if _, err := store.PagePayload(ctx, run.ID(), 2); !errors.Is(err, ErrPagePayloadNotFound) {
			t.Fatalf("missing payload error = %v", err)
		}
		summaries, err = store.PageSummaries(ctx, run.ID())
		if err != nil || len(summaries) != 2 || summaries[1].PayloadBytes != 0 {
			t.Fatalf("summaries after second page = %+v err=%v", summaries, err)
		}
	})
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
	target, err := store.CreateSummaryTarget(ctx, collection.Platform(platform), appID, pageFixtureSide(platform), collection.DesiredEnabled)
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := store.CreateSummaryRun(ctx, target.ID(), target.SwitchVersion(), mustCollectionCursor(t, nil))
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
