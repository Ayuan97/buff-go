package postgres

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"buff-go/internal/catalog"
	"buff-go/internal/collection"
	"buff-go/internal/market"
	"buff-go/internal/resource"
)

func testCollectionSummaryPages(t *testing.T, dsn string) {
	t.Run("atomic page and market lifecycle", func(t *testing.T) {
		store, db := migratedStore(t, dsn)
		ctx := t.Context()
		target, task, combination, products := summaryPageFixture(t, store, 730, "page-lifecycle", 2)
		collectedAt := time.Now().UTC().Truncate(time.Microsecond)
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
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CursorBefore: mustCollectionCursor(t, nil), CursorAfter: next,
			CollectedAt: collectedAt,
			Attempts: []AttemptWrite{
				{ProductID: products[0].ProductID, Observation: present},
				{ProductID: products[1].ProductID, Observation: failed, ReasonCode: "fetch.timeout"},
			},
		}
		page, applied, err := store.CommitSummaryPage(ctx, input)
		if err != nil || !applied || page.WriteSeq() != 1 || page.TargetID() != target.ID() {
			t.Fatalf("CommitSummaryPage() page=%+v applied=%v err=%v", page, applied, err)
		}
		stored, found, err := store.Target(ctx, target.ID())
		if err != nil || !found || stored.WriteSeq() != 1 {
			t.Fatalf("target after commit = %+v found=%v err=%v", stored, found, err)
		}
		wantOrder := market.WriteOrder{SwitchVersion: int64(target.SwitchVersion()), WriteSequence: 1}
		latest, found, err := store.LatestAttempt(ctx, MarketKey{ProductID: products[0].ProductID, Platform: "page-lifecycle", Side: market.SideAsk})
		if err != nil || !found || latest.Order != wantOrder {
			t.Fatalf("latest = %+v found=%v err=%v", latest, found, err)
		}
		last, found, err := store.LastPresent(ctx, latest.Key)
		if err != nil || !found || last.Observation.Summary == nil || last.Observation.Summary.PriceCents != price {
			t.Fatalf("last present = %+v found=%v err=%v", last, found, err)
		}
		assertWorkerSeesPageItems(t, store, combination.ID, target.ID(), products[0].ProductID)

		restarted, err := New(db)
		if err != nil {
			t.Fatal(err)
		}
		restored, found, err := restarted.LatestAttempt(ctx, latest.Key)
		if err != nil || !found || restored.Order != wantOrder {
			t.Fatalf("restart latest = %+v found=%v err=%v", restored, found, err)
		}
	})

	t.Run("fence and transaction rollback", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		target, task, combination, products := summaryPageFixture(t, store, 730, "page-fence", 1)
		wrongApp, err := store.CreateSteamProduct(ctx, 440, "wrong-app")
		if err != nil {
			t.Fatal(err)
		}
		collectedAt := time.Now().UTC().Truncate(time.Microsecond)
		valid := market.Observation{Side: market.SideBid, Status: market.StatusEmpty, CollectedAt: collectedAt}
		invalidScope := SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CollectedAt: collectedAt,
			Attempts: []AttemptWrite{
				{ProductID: products[0].ProductID, Observation: valid},
				{ProductID: wrongApp.ProductID, Observation: valid},
			},
		}
		wrongOwner := invalidScope
		wrongOwner.CombinationID = combination.ID + 1
		wrongOwner.Attempts = []AttemptWrite{{ProductID: products[0].ProductID, Observation: valid}}
		if _, _, err := store.CommitSummaryPage(ctx, wrongOwner); !errors.Is(err, ErrCollectionFence) {
			t.Fatalf("wrong claim owner error = %v", err)
		}
		assertNoCommittedSummaryPage(t, store, target, products[0].ProductID)

		if _, _, err := store.CommitSummaryPage(ctx, invalidScope); !errors.Is(err, ErrCollectionInvalidInput) {
			t.Fatalf("cross-app page error = %v", err)
		}
		assertNoCommittedSummaryPage(t, store, target, products[0].ProductID)

		lateAttempt := valid
		lateAttempt.CollectedAt = collectedAt.Add(time.Microsecond)
		if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CollectedAt: collectedAt,
			Attempts:    []AttemptWrite{{ProductID: products[0].ProductID, Observation: lateAttempt}},
		}); !errors.Is(err, ErrCollectionInvalidInput) {
			t.Fatalf("attempt after page error = %v", err)
		}
		assertNoCommittedSummaryPage(t, store, target, products[0].ProductID)

		oldSwitch := target.SwitchVersion()
		target, err = store.SetTargetDesired(ctx, target.ID(), target.Revision(), collection.DesiredDisabled)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: oldSwitch, CollectedAt: collectedAt,
		}); !errors.Is(err, ErrCollectionFence) {
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
		if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: oldSwitch, CollectedAt: collectedAt,
		}); !errors.Is(err, ErrCollectionFence) {
			t.Fatalf("old-switch page error = %v", err)
		}
		assertNoCommittedSummaryPage(t, store, target, products[0].ProductID)
	})

	t.Run("zero ask total resets queued scan", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		target, task, _, _ := summaryPageFixture(t, store, 730, "page-zero", 0)
		cursor, err := collection.EncodeAskRefill(40)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.EnqueueTasks(ctx, target.ID(), target.SwitchVersion(), mustAskPageSpecs(t, 4), cursor, 0); err != nil {
			t.Fatal(err)
		}
		_, secondCombination := mustQueueCombination(t, store, "page-zero")
		secondTask, _, claimed, err := store.ClaimTask(ctx, secondCombination.ID, "page-zero")
		if err != nil || !claimed {
			t.Fatalf("second claim task=%+v claimed=%v err=%v", secondTask, claimed, err)
		}
		collectedAt := time.Now().UTC().Truncate(time.Microsecond)
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(),
			ExpectedSwitch: target.SwitchVersion(), CollectedAt: collectedAt, AskTotal: 0,
		}); err != nil || !applied {
			t.Fatalf("zero-total commit applied=%v err=%v", applied, err)
		}
		if depth, err := store.QueueDepth(ctx, target.ID()); err != nil || depth != 2 {
			t.Fatalf("queue after zero total depth=%d err=%v", depth, err)
		}
		stored, found, err := store.Target(ctx, target.ID())
		if err != nil || !found || stored.RefillTotal() != 0 {
			t.Fatalf("target after zero total = %+v found=%v err=%v", stored, found, err)
		}
		start, err := collection.DecodeAskRefill(stored.RefillCursor())
		if err != nil || start != 0 {
			t.Fatalf("refill cursor start=%d err=%v", start, err)
		}
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: secondTask.ID(), CombinationID: secondCombination.ID, ClaimGeneration: secondTask.ClaimGeneration(),
			ExpectedSwitch: target.SwitchVersion(), CollectedAt: collectedAt.Add(time.Microsecond), AskTotal: 10,
		}); err != nil || !applied {
			t.Fatalf("preserved claim commit applied=%v err=%v", applied, err)
		}
	})

	t.Run("orphan market facts", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		target, task, _, products := summaryPageFixture(t, store, 252490, "page-orphan", 1)
		collectedAt := time.Now().UTC().Truncate(time.Microsecond)
		observation := market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt}
		order := market.WriteOrder{
			SwitchVersion: int64(target.SwitchVersion()), WriteSequence: 1,
		}
		if applied, err := store.saveObservations(ctx, observationBatch{
			AppID: 252490, Platform: "page-orphan", Side: market.SideAsk, Order: order,
			Attempts: []AttemptWrite{{ProductID: products[0].ProductID, Observation: observation}},
		}); err != nil || !applied {
			t.Fatalf("seed orphan market fact applied=%v err=%v", applied, err)
		}
		if _, _, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CollectedAt: collectedAt,
			Attempts:    []AttemptWrite{{ProductID: products[0].ProductID, Observation: observation}},
		}); !errors.Is(err, ErrCollectionIntegrity) {
			t.Fatalf("orphan market fact error = %v", err)
		}
		assertNoLatestPage(t, store, target.ID())
		stored, found, err := store.Target(ctx, target.ID())
		if err != nil || !found || stored.WriteSeq() != 0 {
			t.Fatalf("orphaned commit must not advance write_seq = %+v found=%v err=%v", stored, found, err)
		}
	})

	t.Run("exact name creates product", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		target, task, _, _ := summaryPageFixture(t, store, 730, "page-name", 0)
		collectedAt := time.Now().UTC().Truncate(time.Microsecond)
		observation := market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt}
		page, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CollectedAt: collectedAt,
			Attempts:    []AttemptWrite{{ExactName: "AK-47 | Redline", Observation: observation}},
		})
		if err != nil || !applied || page.WriteSeq() != 1 {
			t.Fatalf("CommitSummaryPage() page=%+v applied=%v err=%v", page, applied, err)
		}
		products, err := store.ListSteamProductsByAppID(ctx, 730)
		if err != nil || len(products) != 1 || products[0].Name != "AK-47 | Redline" {
			t.Fatalf("created products = %+v err=%v", products, err)
		}
		latest, found, err := store.LatestAttempt(ctx, MarketKey{ProductID: products[0].ProductID, Platform: "page-name", Side: market.SideAsk})
		if err != nil || !found || latest.Status != market.StatusEmpty || latest.Order.WriteSequence != 1 {
			t.Fatalf("latest = %+v found=%v err=%v", latest, found, err)
		}
	})

	// 展示元数据必须能补到早先建好的商品上，否则老商品永远没有图标。
	t.Run("product media backfill and attribution", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		target, task, _, _ := summaryPageFixture(t, store, 730, "page-media", 0)
		collectedAt := time.Now().UTC().Truncate(time.Microsecond)
		observation := market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt}

		// 第一页不带元数据，模拟本次迁移之前建立的商品
		first, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CollectedAt: collectedAt,
			Attempts:    []AttemptWrite{{ExactName: "AK-47 | Redline", Observation: observation}},
		})
		if err != nil || !applied {
			t.Fatalf("commit without media applied=%v err=%v", applied, err)
		}
		if first.AccountID() != 0 || first.ExitAddress().IsValid() {
			t.Fatalf("first page attribution = %+v", first)
		}
		bare, err := store.ListMarketQuotes(ctx, MarketQuoteFilter{AppID: 730, Platform: "page-media", Limit: 10})
		if err != nil || len(bare.Quotes) != 1 || !bare.Quotes[0].Media.Empty() {
			t.Fatalf("quotes before backfill = %+v err=%v", bare.Quotes, err)
		}

		// 同一认领任务再提交一页，带上元数据，同一个商品应被补齐而不是新建
		next := mustCollectionCursor(t, []byte("2"))
		media := catalog.ProductMedia{IconPath: "iconAK47", ItemType: "Rifle", NameColor: "d2d2d2"}
		secondCollected := collectedAt.Add(time.Second)
		second, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CursorBefore: mustCollectionCursor(t, nil), CursorAfter: next,
			CollectedAt: secondCollected,
			Attempts: []AttemptWrite{{
				ExactName:   "AK-47 | Redline",
				Observation: market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: secondCollected},
				Media:       media,
			}},
			AccountID:   77,
			ExitAddress: netip.MustParseAddr("38.175.103.188"),
		})
		if err != nil || !applied || second.WriteSeq() != 2 {
			t.Fatalf("commit with media applied=%v write_seq=%d err=%v", applied, second.WriteSeq(), err)
		}
		if second.AccountID() != 77 || second.ExitAddress().String() != "38.175.103.188" {
			t.Fatalf("second page attribution = %+v", second)
		}
		filled, err := store.ListMarketQuotes(ctx, MarketQuoteFilter{AppID: 730, Platform: "page-media", Limit: 10})
		if err != nil || len(filled.Quotes) != 1 || filled.Quotes[0].Media != media {
			t.Fatalf("quotes after backfill = %+v err=%v", filled.Quotes, err)
		}

		// 非法元数据必须降级为空，不能让整页提交失败
		third := mustCollectionCursor(t, []byte("3"))
		thirdCollected := collectedAt.Add(2 * time.Second)
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CursorBefore: next, CursorAfter: third,
			CollectedAt: thirdCollected,
			Attempts: []AttemptWrite{{
				ExactName:   "AWP | Asiimov",
				Observation: market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: thirdCollected},
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
		target, task, _, products := summaryPageFixture(t, store, 730, "page-payload", 1)
		collectedAt := time.Now().UTC().Truncate(time.Microsecond)
		observation := market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: collectedAt}
		raw := []byte(`{"success":true,"total_count":35252,"results":[]}`)
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CollectedAt: collectedAt,
			Attempts:    []AttemptWrite{{ProductID: products[0].ProductID, Observation: observation}},
			Payload:     raw,
		}); err != nil || !applied {
			t.Fatalf("commit with payload applied=%v err=%v", applied, err)
		}
		storedRaw, payloadBytes := mustLatestPagePayload(t, store, target.ID())
		if string(storedRaw) != string(raw) || payloadBytes != int64(len(raw)) {
			t.Fatalf("payload = %q bytes=%d", storedRaw, payloadBytes)
		}

		// 没有 payload 的方向提交后，最新页在但副本不在
		next := mustCollectionCursor(t, []byte("2"))
		secondCollected := collectedAt.Add(time.Second)
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CursorBefore: mustCollectionCursor(t, nil), CursorAfter: next,
			CollectedAt: secondCollected,
			Attempts:    []AttemptWrite{{ProductID: products[0].ProductID, Observation: market.Observation{Side: market.SideAsk, Status: market.StatusEmpty, CollectedAt: secondCollected}}},
		}); err != nil || !applied {
			t.Fatalf("commit without payload applied=%v err=%v", applied, err)
		}
		emptyRaw, emptyBytes := mustLatestPagePayload(t, store, target.ID())
		if len(emptyRaw) != 0 || emptyBytes != 0 {
			t.Fatalf("missing payload leftover = %q bytes=%d", emptyRaw, emptyBytes)
		}
	})

	t.Run("price ticks record cent changes only", func(t *testing.T) {
		store, _ := migratedStore(t, dsn)
		ctx := t.Context()
		target, task, _, products := summaryPageFixture(t, store, 730, "page-ticks", 2)
		collectedAt := time.Now().UTC().Truncate(time.Microsecond)
		price := market.CNYCents(100)
		present, err := market.NewPresentObservation(market.PresentInput{
			Currency: market.CurrencyCNY, Side: market.SideAsk, PriceCents: &price, CollectedAt: collectedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CollectedAt: collectedAt,
			Attempts: []AttemptWrite{
				{ProductID: products[0].ProductID, Observation: present},
				{ProductID: products[1].ProductID, Observation: present},
			},
		}); err != nil || !applied {
			t.Fatalf("first present applied=%v err=%v", applied, err)
		}
		ticks, err := store.ListPriceTicks(ctx, PriceTickFilter{ProductID: int64(products[0].ProductID), Limit: 10})
		if err != nil || len(ticks) != 1 || ticks[0].PrevCents != nil || ticks[0].PriceCents != 100 {
			t.Fatalf("first tick = %+v err=%v", ticks, err)
		}

		orders := int64(9)
		samePrice, err := market.NewPresentObservation(market.PresentInput{
			Currency: market.CurrencyCNY, Side: market.SideAsk, PriceCents: &price, OrderCount: &orders,
			CollectedAt: collectedAt.Add(time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		next := mustCollectionCursor(t, []byte("2"))
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CursorBefore: mustCollectionCursor(t, nil), CursorAfter: next,
			CollectedAt: collectedAt.Add(time.Second),
			Attempts:    []AttemptWrite{{ProductID: products[0].ProductID, Observation: samePrice}},
		}); err != nil || !applied {
			t.Fatalf("same-cent present applied=%v err=%v", applied, err)
		}
		ticks, err = store.ListPriceTicks(ctx, PriceTickFilter{ProductID: int64(products[0].ProductID), Limit: 10})
		if err != nil || len(ticks) != 1 {
			t.Fatalf("order-count change must not add tick = %+v err=%v", ticks, err)
		}

		changed := market.CNYCents(110)
		newPrice, err := market.NewPresentObservation(market.PresentInput{
			Currency: market.CurrencyCNY, Side: market.SideAsk, PriceCents: &changed,
			CollectedAt: collectedAt.Add(2 * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		third := mustCollectionCursor(t, []byte("3"))
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CursorBefore: next, CursorAfter: third,
			CollectedAt: collectedAt.Add(2 * time.Second),
			Attempts:    []AttemptWrite{{ProductID: products[0].ProductID, Observation: newPrice}},
		}); err != nil || !applied {
			t.Fatalf("changed present applied=%v err=%v", applied, err)
		}
		ticks, err = store.ListPriceTicks(ctx, PriceTickFilter{ProductID: int64(products[0].ProductID), Limit: 10})
		if err != nil || len(ticks) != 2 || ticks[0].PrevCents == nil || *ticks[0].PrevCents != 100 || ticks[0].PriceCents != 110 {
			t.Fatalf("changed ticks = %+v err=%v", ticks, err)
		}

		decreased := market.CNYCents(90)
		downPrice, err := market.NewPresentObservation(market.PresentInput{
			Currency: market.CurrencyCNY, Side: market.SideAsk, PriceCents: &decreased,
			CollectedAt: collectedAt.Add(3 * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		fourth := mustCollectionCursor(t, []byte("4"))
		if _, applied, err := store.CommitSummaryPage(ctx, SummaryPageCommit{
			TargetID: target.ID(), TaskID: task.ID(), CombinationID: claimedCombination(t, task), ClaimGeneration: task.ClaimGeneration(), ExpectedSwitch: target.SwitchVersion(),
			CursorBefore: third, CursorAfter: fourth,
			CollectedAt: collectedAt.Add(3 * time.Second),
			Attempts:    []AttemptWrite{{ProductID: products[0].ProductID, Observation: downPrice}},
		}); err != nil || !applied {
			t.Fatalf("downward price applied=%v err=%v", applied, err)
		}

		ordinary, err := store.ListMarketQuotes(ctx, MarketQuoteFilter{
			ProductID: int64(products[0].ProductID), Platform: "page-ticks", Limit: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		dropped, err := store.ListMarketQuotes(ctx, MarketQuoteFilter{
			AppID: 730, Platform: "page-ticks", DropsOnly: true, Sort: QuoteSortDropDesc, Limit: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		for name, result := range map[string]MarketQuoteResult{"ordinary": ordinary, "dropped": dropped} {
			if result.Total != 1 || len(result.Quotes) != 1 {
				t.Fatalf("%s quote result = %+v", name, result)
			}
			quote := result.Quotes[0]
			if quote.DropCents == nil || *quote.DropCents != 20 || quote.HighCents == nil || *quote.HighCents != 110 ||
				quote.DropPctBP == nil || *quote.DropPctBP != 1818 || quote.DropCount == nil || *quote.DropCount != 1 ||
				quote.LastDropAt == nil || !quote.LastDropAt.Equal(collectedAt.Add(3*time.Second)) {
				t.Fatalf("%s drop stats = %+v", name, quote)
			}
		}
	})

	t.Run("price tick purge advances in batches", func(t *testing.T) {
		store, db := migratedStore(t, dsn)
		ctx := t.Context()
		product, err := store.CreateSteamProduct(ctx, 730, "purge-price-ticks")
		if err != nil {
			t.Fatal(err)
		}
		oldAt := time.Now().UTC().Add(-PriceTickRetention - time.Hour)
		if _, err := db.ExecContext(ctx, `
INSERT INTO market_price_ticks (
    product_id, platform, side, prev_cents, price_cny_cents, collected_at, switch_version, write_seq
)
SELECT $1, 'purge-test', 'ask', 100, 101, $2, 1, value
FROM generate_series(1, 5001) AS value`, int64(product.ProductID), oldAt); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `
INSERT INTO market_price_ticks (
    product_id, platform, side, prev_cents, price_cny_cents, collected_at, switch_version, write_seq
)
VALUES ($1, 'purge-test', 'ask', 101, 102, $2, 1, 6000)`, int64(product.ProductID), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if err := store.PurgeExpiredPriceTicks(ctx, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		var remaining int
		if err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM market_price_ticks WHERE platform = 'purge-test'`).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 1 {
			t.Fatalf("remaining price ticks = %d, want recent row only", remaining)
		}
	})
}

func summaryPageFixture(t *testing.T, store *Store, appID int64, platform string, productCount int) (collection.Target, collection.Task, resource.AccountNodeCombination, []catalog.SteamProduct) {
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
	target, task, combination := queueCommitFixture(t, store, collection.Platform(platform), appID, pageFixtureSide(platform))
	return target, task, combination, products
}

func pageFixtureSide(platform string) market.Side {
	if platform == "page-fence" {
		return market.SideBid
	}
	return market.SideAsk
}

func assertNoCommittedSummaryPage(t *testing.T, store *Store, target collection.Target, productID catalog.ProductID) {
	t.Helper()
	stored, found, err := store.Target(t.Context(), target.ID())
	if err != nil || !found || stored.WriteSeq() != 0 {
		t.Fatalf("target changed = %+v found=%v err=%v", stored, found, err)
	}
	assertNoLatestPage(t, store, target.ID())
	side, ok := target.Side()
	if !ok {
		t.Fatal("summary target has no side")
	}
	if _, found, err := store.LatestAttempt(t.Context(), MarketKey{ProductID: productID, Platform: string(target.Platform()), Side: side}); err != nil || found {
		t.Fatalf("market fact found=%v err=%v", found, err)
	}
}

func assertNoLatestPage(t *testing.T, store *Store, id collection.TargetID) {
	t.Helper()
	var n int
	if err := store.db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM collection_latest_pages WHERE target_id = $1`, int64(id),
	).Scan(&n); err != nil || n != 0 {
		t.Fatalf("latest pages = %d err=%v", n, err)
	}
}

func mustLatestPagePayload(t *testing.T, store *Store, id collection.TargetID) ([]byte, int64) {
	t.Helper()
	var compressed []byte
	var payloadBytes *int64
	if err := store.db.QueryRowContext(t.Context(),
		`SELECT payload_gzip, payload_bytes FROM collection_latest_pages WHERE target_id = $1`, int64(id),
	).Scan(&compressed, &payloadBytes); err != nil {
		t.Fatalf("read latest page payload: %v", err)
	}
	if len(compressed) == 0 {
		return nil, 0
	}
	raw, err := gunzipPayload(compressed)
	if err != nil {
		t.Fatalf("gunzip latest page payload: %v", err)
	}
	if payloadBytes == nil {
		t.Fatal("payload_bytes is null while gzip is present")
	}
	return raw, *payloadBytes
}

func assertWorkerSeesPageItems(t *testing.T, store *Store, combinationID resource.CombinationID, targetID collection.TargetID, productID catalog.ProductID) {
	t.Helper()
	workers, err := store.ListWorkers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, worker := range workers {
		if worker.Combination.ID != combinationID {
			continue
		}
		if worker.Idle || worker.Claim == nil || worker.Claim.TargetID != targetID {
			t.Fatalf("worker claim = %+v idle=%v", worker.Claim, worker.Idle)
		}
		if productID < 1 {
			if len(worker.Claim.Items) == 0 {
				t.Fatal("worker claim has no page items")
			}
			return
		}
		for _, item := range worker.Claim.Items {
			if item.ProductID == int64(productID) {
				return
			}
		}
		t.Fatalf("worker items = %+v, want product %d", worker.Claim.Items, productID)
	}
	t.Fatalf("combination %d missing from ListWorkers", combinationID)
}
