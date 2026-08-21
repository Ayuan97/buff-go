package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"strings"
	"testing"
	"time"

	"buff-go/internal/resource"
)

func testResourceStorage(t *testing.T, dsn string) {
	db := newTestSchema(t, dsn)
	if err := ApplyMigrations(t.Context(), db); err != nil {
		t.Fatalf("migrate resources: %v", err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("accounts", func(t *testing.T) { testAccountResources(t, store, db) })
	t.Run("nodes", func(t *testing.T) { testNodeResources(t, store, db) })
	t.Run("combinations", func(t *testing.T) { testCombinationResources(t, store, db) })
	t.Run("database constraints", func(t *testing.T) { testResourceDatabaseConstraints(t, db) })
	t.Run("closed storage errors", func(t *testing.T) { testClosedResourceErrors(t, dsn) })
}

func testAccountResources(t *testing.T, store *Store, db queryExecer) {
	ctx := t.Context()
	session := []byte("synthetic-account-session-marker")
	account, err := store.CreateAccount(ctx, "steam", "steam-main", session)
	if err != nil {
		t.Fatal(err)
	}
	if account.SessionRevision != 1 || account.SessionState != resource.AccountSessionStateUnverified || account.LastCheckedAt != nil {
		t.Fatalf("new account = %+v", account)
	}
	opened, err := store.OpenAccountSessionAt(ctx, account.ID, account.SessionRevision)
	if err != nil || !bytes.Equal(opened, session) {
		t.Fatalf("open account session = %q err=%v", opened, err)
	}
	assertMarkerPlaintext(t, db, "platform_accounts", "session_plaintext", "account_id", int64(account.ID), session)

	restarted, err := New(store.db)
	if err != nil {
		t.Fatal(err)
	}
	opened, err = restarted.OpenAccountSessionAt(ctx, account.ID, account.SessionRevision)
	if err != nil || !bytes.Equal(opened, session) {
		t.Fatalf("restart open = %q err=%v", opened, err)
	}

	replacement := []byte("synthetic-replaced-account-session")
	account, err = store.ReplaceAccountSession(ctx, account.ID, 1, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if account.SessionRevision != 2 || account.SessionState != resource.AccountSessionStateUnverified {
		t.Fatalf("replaced account = %+v", account)
	}
	if _, err := store.OpenAccountSessionAt(ctx, account.ID, 1); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("stale account credential open error = %v", err)
	}
	if opened, err := store.OpenAccountSessionAt(ctx, account.ID, account.SessionRevision); err != nil || !bytes.Equal(opened, replacement) {
		t.Fatalf("replacement account credential = %q err=%v", opened, err)
	}
	assertMarkerPlaintext(t, db, "platform_accounts", "session_plaintext", "account_id", int64(account.ID), replacement)
	if _, err := store.RecordAccountSessionCheck(ctx, account.ID, 1, resource.AccountSessionStateValid, resourceTime()); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("old revision validation error = %v", err)
	}
	checkedAt := resourceTime().Add(time.Minute)
	account, err = store.RecordAccountSessionCheck(ctx, account.ID, 2, resource.AccountSessionStateValid, checkedAt)
	if err != nil || account.LastCheckedAt == nil || !account.LastCheckedAt.Equal(checkedAt) {
		t.Fatalf("record account check = %+v err=%v", account, err)
	}
	if _, err := store.RecordAccountSessionCheck(ctx, account.ID, 2, resource.AccountSessionStateInvalid, checkedAt.Add(-time.Second)); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("older check error = %v", err)
	}
	if _, err := store.RecordAccountSessionCheck(ctx, account.ID, 2, resource.AccountSessionStateInvalid, checkedAt); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("equal-time conflicting check error = %v", err)
	}
	if _, err := store.RecordAccountSessionCheck(ctx, account.ID, 2, resource.AccountSessionStateValid, checkedAt); err != nil {
		t.Fatalf("equal check retry: %v", err)
	}

	other, err := store.CreateAccount(ctx, "steam", "steam-other", []byte("synthetic-other-account-session"))
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == account.ID {
		t.Fatal("duplicate account identity")
	}

	old, err := store.CreateAccount(ctx, "buff", "recreated-alias", []byte("synthetic-old-id-session"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM platform_accounts WHERE account_id = $1`, int64(old.ID)); err != nil {
		t.Fatal(err)
	}
	recreated, err := store.CreateAccount(ctx, "buff", "recreated-alias", []byte("synthetic-new-id-session"))
	if err != nil {
		t.Fatal(err)
	}
	if recreated.ID == old.ID {
		t.Fatal("deleted account identity was reused")
	}
	if opened, err := store.OpenAccountSessionAt(ctx, recreated.ID, recreated.SessionRevision); err != nil ||
		!bytes.Equal(opened, []byte("synthetic-new-id-session")) {
		t.Fatalf("recreated account session = %q err=%v", opened, err)
	}

	accounts, err := store.ListAccounts(ctx)
	if err != nil || len(accounts) < 3 {
		t.Fatalf("accounts count=%d err=%v", len(accounts), err)
	}
	publicStore, err := New(store.db)
	if err != nil {
		t.Fatal(err)
	}
	plainSession := []byte("synthetic-plain-new-store")
	plainAccount, err := publicStore.CreateAccount(ctx, "steam", "plain-new-store", plainSession)
	if err != nil {
		t.Fatalf("New() create account: %v", err)
	}
	if opened, err := publicStore.OpenAccountSessionAt(ctx, plainAccount.ID, plainAccount.SessionRevision); err != nil || !bytes.Equal(opened, plainSession) {
		t.Fatalf("New() open account = %q err=%v", opened, err)
	}
	errorMarker := []byte("synthetic-storage-error-secret-marker")
	if _, err := store.CreateAccount(ctx, "steam", "steam-main", errorMarker); err == nil || bytes.Contains([]byte(err.Error()), errorMarker) {
		t.Fatalf("duplicate account error leaked secret or was nil: %v", err)
	}

	concurrent, err := store.CreateAccount(ctx, "steam", "concurrent-check", []byte("synthetic-concurrent-session"))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	type accountCheckResult struct {
		state resource.AccountSessionState
		err   error
	}
	results := make(chan accountCheckResult, 2)
	raceTime := checkedAt.Add(2 * time.Minute)
	go func() {
		<-start
		_, err := store.RecordAccountSessionCheck(ctx, concurrent.ID, 1, resource.AccountSessionStateInvalid, raceTime)
		results <- accountCheckResult{state: resource.AccountSessionStateInvalid, err: err}
	}()
	go func() {
		<-start
		_, err := store.RecordAccountSessionCheck(ctx, concurrent.ID, 1, resource.AccountSessionStateValid, raceTime)
		results <- accountCheckResult{state: resource.AccountSessionStateValid, err: err}
	}()
	close(start)
	firstResult, secondResult := <-results, <-results
	if (firstResult.err == nil) == (secondResult.err == nil) {
		t.Fatalf("same-time account check errors = %v, %v", firstResult.err, secondResult.err)
	}
	winner := firstResult
	loser := secondResult
	if winner.err != nil {
		winner, loser = loser, winner
	}
	if !errors.Is(loser.err, ErrResourceRevisionConflict) {
		t.Fatalf("same-time losing account check error = %v", loser.err)
	}
	if winner.err != nil {
		t.Fatalf("same-time winning account check error = %v", winner.err)
	}
	finalAccount, found, err := store.Account(ctx, concurrent.ID)
	if err != nil || !found || finalAccount.SessionState != winner.state ||
		finalAccount.LastCheckedAt == nil || !finalAccount.LastCheckedAt.Equal(raceTime) {
		t.Fatalf("same-time account final = %+v winner=%s found=%v err=%v", finalAccount, winner.state, found, err)
	}
	if winner.state == loser.state {
		t.Fatal("account race did not use conflicting states")
	}

	replaceRace, err := store.CreateAccount(ctx, "steam", "replace-check-race", []byte("synthetic-race-old-session"))
	if err != nil {
		t.Fatal(err)
	}
	type replaceCheckResult struct {
		operation string
		err       error
	}
	replaceStart := make(chan struct{})
	replaceResults := make(chan replaceCheckResult, 2)
	go func() {
		<-replaceStart
		_, err := store.ReplaceAccountSession(ctx, replaceRace.ID, 1, []byte("synthetic-race-new-session"))
		replaceResults <- replaceCheckResult{operation: "replace", err: err}
	}()
	go func() {
		<-replaceStart
		_, err := store.RecordAccountSessionCheck(ctx, replaceRace.ID, 1, resource.AccountSessionStateValid, raceTime)
		replaceResults <- replaceCheckResult{operation: "check", err: err}
	}()
	close(replaceStart)
	for range 2 {
		result := <-replaceResults
		if result.operation == "replace" && result.err != nil {
			t.Fatalf("concurrent replacement error = %v", result.err)
		}
		if result.operation == "check" && result.err != nil && !errors.Is(result.err, ErrResourceRevisionConflict) {
			t.Fatalf("concurrent old check error = %v", result.err)
		}
	}
	replacedRace, found, err := store.Account(ctx, replaceRace.ID)
	if err != nil || !found || replacedRace.SessionRevision != 2 ||
		replacedRace.SessionState != resource.AccountSessionStateUnverified || replacedRace.LastCheckedAt != nil {
		t.Fatalf("replace/check race final = %+v found=%v err=%v", replacedRace, found, err)
	}
}

func testNodeResources(t *testing.T, store *Store, db queryExecer) {
	ctx := t.Context()
	now := resourceTime()
	publicStore, err := New(store.db)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := publicStore.CreateNode(ctx, "local-direct", resource.NodeConnectionInput{
		Kind: resource.NodeKindDirect, Region: resource.NodeRegionDomestic, EgressMode: resource.EgressModeStatic,
	})
	if err != nil {
		t.Fatal(err)
	}
	if direct.HasProxyCredential || direct.State != resource.NodeStateValidating || direct.EgressRevision != 1 {
		t.Fatalf("direct node = %+v", direct)
	}
	if _, err := store.OpenNodeProxyCredentialAt(ctx, direct.ID, direct.EgressRevision); !errors.Is(err, ErrProxyCredentialUnavailable) {
		t.Fatalf("direct proxy credential error = %v", err)
	}
	if _, err := store.CreateNode(ctx, "proxy-empty", resource.NodeConnectionInput{
		Kind: resource.NodeKindProxy, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic,
	}); err == nil {
		t.Fatal("proxy without opaque connection material was accepted")
	}

	renamed, err := publicStore.RenameNode(ctx, direct.ID, "local-direct-renamed")
	if err != nil || renamed.Name != "local-direct-renamed" ||
		renamed.EgressRevision != direct.EgressRevision {
		t.Fatalf("rename node = %+v err=%v", renamed, err)
	}
	if same, err := publicStore.RenameNode(ctx, direct.ID, "local-direct-renamed"); err != nil || same.Name != renamed.Name {
		t.Fatalf("idempotent rename = %+v err=%v", same, err)
	}
	if _, err := publicStore.RenameNode(ctx, direct.ID, ""); !errors.Is(err, ErrInvalidResource) {
		t.Fatalf("empty rename = %v", err)
	}
	if _, err := publicStore.RenameNode(ctx, 9999, "missing"); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("rename missing node = %v", err)
	}
	direct = renamed

	proxySecret := []byte("https://synthetic-user:synthetic-pass@proxy.invalid:8443?sticky=abc")
	proxy, err := store.CreateNode(ctx, "foreign-proxy", resource.NodeConnectionInput{
		Kind: resource.NodeKindProxy, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic, ProxyCredential: proxySecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !proxy.HasProxyCredential {
		t.Fatal("proxy safe model does not report credential presence")
	}
	errorMarker := []byte("http://synthetic-node-storage-error-secret-marker@proxy.invalid:8080")
	if _, err := store.CreateNode(ctx, "foreign-proxy", resource.NodeConnectionInput{
		Kind: resource.NodeKindProxy, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic, ProxyCredential: errorMarker,
	}); err == nil || bytes.Contains([]byte(err.Error()), errorMarker) {
		t.Fatalf("duplicate node error leaked secret or was nil: %v", err)
	}
	opened, err := store.OpenNodeProxyCredentialAt(ctx, proxy.ID, proxy.EgressRevision)
	if err != nil || !bytes.Equal(opened, proxySecret) {
		t.Fatalf("open proxy credential = %q err=%v", opened, err)
	}
	assertMarkerPlaintext(t, db, "access_nodes", "proxy_plaintext", "node_id", int64(proxy.ID), proxySecret)
	if _, err := publicStore.RenameNode(ctx, direct.ID, proxy.Name); !errors.Is(err, ErrNodeNameConflict) {
		t.Fatalf("rename into existing name = %v", err)
	}

	restarted, err := New(store.db)
	if err != nil {
		t.Fatal(err)
	}
	if opened, err := restarted.OpenNodeProxyCredentialAt(ctx, proxy.ID, proxy.EgressRevision); err != nil || !bytes.Equal(opened, proxySecret) {
		t.Fatalf("restart open proxy = %q err=%v", opened, err)
	}
	if opened, err := publicStore.OpenNodeProxyCredentialAt(ctx, proxy.ID, proxy.EgressRevision); err != nil || !bytes.Equal(opened, proxySecret) {
		t.Fatalf("New() open proxy = %q err=%v", opened, err)
	}

	replacedSecret := []byte("http://synthetic-replaced:proxy-material@proxy.invalid:8080")
	proxy, err = store.ReplaceNodeConnection(ctx, proxy.ID, 1, resource.NodeConnectionInput{
		Kind: resource.NodeKindProxy, Region: resource.NodeRegionHongKong, EgressMode: resource.EgressModeStatic, ProxyCredential: replacedSecret,
	})
	if err != nil || proxy.EgressRevision != 2 || proxy.State != resource.NodeStateValidating {
		t.Fatalf("replace proxy = %+v err=%v", proxy, err)
	}
	if _, err := store.OpenNodeProxyCredentialAt(ctx, proxy.ID, 1); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("stale node credential open error = %v", err)
	}
	if _, err := store.RecordNodeExit(ctx, proxy.ID, 1, netip.MustParseAddr("1.1.1.1"), now, now.Add(time.Hour)); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("stale node verification error = %v", err)
	}
	proxy, err = store.BeginNodeRevalidation(ctx, proxy.ID, 2)
	if err != nil || proxy.EgressRevision != 3 {
		t.Fatalf("begin revalidation = %+v err=%v", proxy, err)
	}
	if opened, err := store.OpenNodeProxyCredentialAt(ctx, proxy.ID, proxy.EgressRevision); err != nil || !bytes.Equal(opened, replacedSecret) {
		t.Fatalf("revalidation did not preserve proxy material = %q err=%v", opened, err)
	}
	assertMarkerPlaintext(t, db, "access_nodes", "proxy_plaintext", "node_id", int64(proxy.ID), replacedSecret)
	proxy, err = store.RecordNodeExit(ctx, proxy.ID, 3, netip.MustParseAddr("1.1.1.1"), now, now.Add(time.Hour))
	if err != nil || !proxy.UsableAt(now.Add(time.Minute)) || proxy.UsableAt(now.Add(time.Hour)) {
		t.Fatalf("recorded proxy exit = %+v err=%v", proxy, err)
	}
	if _, err := store.MarkNodeUnavailable(ctx, proxy.ID, 3); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("late unavailable result error = %v", err)
	}

	direct, err = store.RecordNodeExit(ctx, direct.ID, 1, netip.MustParseAddr("1.1.1.1"), now, now.Add(30*time.Minute))
	if err != nil || !direct.UsableAt(now.Add(time.Minute)) {
		t.Fatalf("shared exit address rejected = %+v err=%v", direct, err)
	}
	oldDirect := direct
	if _, err := store.ConfirmNodeExit(ctx, direct.ID, 1, netip.MustParseAddr("203.0.113.9"), now, now.Add(time.Hour)); err == nil {
		t.Fatal("documentation exit address was accepted")
	}
	unchangedDirect, found, err := store.Node(ctx, direct.ID)
	if err != nil || !found || unchangedDirect.EgressRevision != oldDirect.EgressRevision ||
		unchangedDirect.ExitVerification == nil || unchangedDirect.ExitVerification.Address != oldDirect.ExitVerification.Address {
		t.Fatalf("invalid confirmation changed node = %+v found=%v err=%v", unchangedDirect, found, err)
	}
	direct, err = store.ConfirmNodeExit(ctx, direct.ID, 1, netip.MustParseAddr("8.8.4.4"), now, now.Add(time.Hour))
	if err != nil || direct.EgressRevision != 2 || direct.ExitVerification == nil ||
		direct.ExitVerification.VerifiedRevision != 2 || direct.ExitVerification.Address != netip.MustParseAddr("8.8.4.4") {
		t.Fatalf("confirmed direct exit = %+v err=%v", direct, err)
	}

	stickyDeadline := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
	sticky, err := store.CreateNode(ctx, "sticky-proxy", resource.NodeConnectionInput{
		Kind: resource.NodeKindProxy, Region: resource.NodeRegionHongKong, EgressMode: resource.EgressModeSticky,
		StickySessionValidUntil: &stickyDeadline, ProxyCredential: []byte("socks5://synthetic-sticky:proxy-material@proxy.invalid:1080"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordNodeExit(ctx, sticky.ID, 1, netip.MustParseAddr("8.8.8.8"), now, stickyDeadline.Add(time.Second)); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("sticky overrun error = %v", err)
	}
	sticky, err = store.RecordNodeExit(ctx, sticky.ID, 1, netip.MustParseAddr("8.8.8.8"), now, stickyDeadline)
	if err != nil || sticky.UsableAt(stickyDeadline) {
		t.Fatalf("sticky bound node = %+v err=%v", sticky, err)
	}
	if _, err := store.ConfirmNodeExit(ctx, sticky.ID, 1, netip.MustParseAddr("1.0.0.1"), now, stickyDeadline.Add(time.Second)); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("sticky confirmation overrun error = %v", err)
	}
	unchangedSticky, found, err := store.Node(ctx, sticky.ID)
	if err != nil || !found || unchangedSticky.EgressRevision != 1 || unchangedSticky.ExitVerification == nil ||
		unchangedSticky.ExitVerification.Address != netip.MustParseAddr("8.8.8.8") {
		t.Fatalf("sticky overrun changed node = %+v found=%v err=%v", unchangedSticky, found, err)
	}

	failing, err := store.CreateNode(ctx, "failure-first", resource.NodeConnectionInput{
		Kind: resource.NodeKindDirect, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkNodeUnavailable(ctx, failing.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordNodeExit(ctx, failing.ID, 1, netip.MustParseAddr("9.9.9.9"), now, now.Add(time.Hour)); !errors.Is(err, ErrResourceRevisionConflict) {
		t.Fatalf("late success after unavailable error = %v", err)
	}
	failing, err = store.ConfirmNodeExit(ctx, failing.ID, 1, netip.MustParseAddr("9.9.9.9"), now, now.Add(time.Hour))
	if err != nil || failing.State != resource.NodeStateAvailable || failing.EgressRevision != 2 ||
		failing.ExitVerification == nil || failing.ExitVerification.VerifiedRevision != 2 {
		t.Fatalf("confirmed unavailable node = %+v err=%v", failing, err)
	}

	terminalRace, err := store.CreateNode(ctx, "terminal-race", resource.NodeConnectionInput{
		Kind: resource.NodeKindDirect, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	type nodeTerminalResult struct {
		state resource.NodeState
		err   error
	}
	results := make(chan nodeTerminalResult, 2)
	go func() {
		<-start
		_, err := store.RecordNodeExit(ctx, terminalRace.ID, 1, netip.MustParseAddr("9.9.9.9"), now, now.Add(time.Hour))
		results <- nodeTerminalResult{state: resource.NodeStateAvailable, err: err}
	}()
	go func() {
		<-start
		_, err := store.MarkNodeUnavailable(ctx, terminalRace.ID, 1)
		results <- nodeTerminalResult{state: resource.NodeStateUnavailable, err: err}
	}()
	close(start)
	firstResult, secondResult := <-results, <-results
	if (firstResult.err == nil) == (secondResult.err == nil) {
		t.Fatalf("terminal race errors = %v, %v", firstResult.err, secondResult.err)
	}
	winner := firstResult
	loser := secondResult
	if winner.err != nil {
		winner, loser = loser, winner
	}
	if winner.err != nil || !errors.Is(loser.err, ErrResourceRevisionConflict) {
		t.Fatalf("terminal race winner=%v loser=%v", winner.err, loser.err)
	}
	terminal, found, err := store.Node(ctx, terminalRace.ID)
	if err != nil || !found || terminal.State != winner.state {
		t.Fatalf("terminal race node = %+v winner=%s found=%v err=%v", terminal, winner.state, found, err)
	}

	confirmRace, err := store.CreateNode(ctx, "confirm-race", resource.NodeConnectionInput{
		Kind: resource.NodeKindDirect, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic,
	})
	if err != nil {
		t.Fatal(err)
	}
	confirmStart := make(chan struct{})
	confirmResults := make(chan error, 2)
	for _, address := range []netip.Addr{netip.MustParseAddr("1.0.0.1"), netip.MustParseAddr("8.8.4.4")} {
		address := address
		go func() {
			<-confirmStart
			_, err := store.ConfirmNodeExit(ctx, confirmRace.ID, 1, address, now, now.Add(time.Hour))
			confirmResults <- err
		}()
	}
	close(confirmStart)
	firstConfirmErr, secondConfirmErr := <-confirmResults, <-confirmResults
	if (firstConfirmErr == nil) == (secondConfirmErr == nil) {
		t.Fatalf("concurrent confirmation errors = %v, %v", firstConfirmErr, secondConfirmErr)
	}
	if firstConfirmErr != nil && !errors.Is(firstConfirmErr, ErrResourceRevisionConflict) ||
		secondConfirmErr != nil && !errors.Is(secondConfirmErr, ErrResourceRevisionConflict) {
		t.Fatalf("concurrent confirmation errors = %v, %v", firstConfirmErr, secondConfirmErr)
	}
	confirmedRace, found, err := store.Node(ctx, confirmRace.ID)
	if err != nil || !found || confirmedRace.State != resource.NodeStateAvailable || confirmedRace.EgressRevision != 2 {
		t.Fatalf("concurrent confirmation node = %+v found=%v err=%v", confirmedRace, found, err)
	}

	swapTarget, err := store.CreateNode(ctx, "swap-target", resource.NodeConnectionInput{
		Kind: resource.NodeKindProxy, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic,
		ProxyCredential: []byte("http://synthetic-swap:target-material@target.proxy.invalid:8080"),
	})
	if err != nil {
		t.Fatal(err)
	}
	swapSource, err := store.CreateNode(ctx, "swap-source", resource.NodeConnectionInput{
		Kind: resource.NodeKindProxy, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic,
		ProxyCredential: []byte("http://synthetic-swap:source-material@source.proxy.invalid:8080"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
UPDATE access_nodes target
SET proxy_plaintext = source.proxy_plaintext
FROM access_nodes source WHERE target.node_id=$1 AND source.node_id=$2`, int64(swapTarget.ID), int64(swapSource.ID)); err != nil {
		t.Fatal(err)
	}
	if opened, err := store.OpenNodeProxyCredentialAt(ctx, swapTarget.ID, swapTarget.EgressRevision); err != nil ||
		!bytes.Equal(opened, []byte("http://synthetic-swap:source-material@source.proxy.invalid:8080")) {
		t.Fatalf("plaintext swap open = %q err=%v", opened, err)
	}
}

func testCombinationResources(t *testing.T, store *Store, db queryExecer) {
	ctx := t.Context()
	accountOne, err := store.CreateAccount(ctx, "steam", "combination-account-one", []byte("synthetic-combination-account-one"))
	if err != nil {
		t.Fatal(err)
	}
	accountTwo, err := store.CreateAccount(ctx, "steam", "combination-account-two", []byte("synthetic-combination-account-two"))
	if err != nil {
		t.Fatal(err)
	}
	buffAccount, err := store.CreateAccount(ctx, "buff", "combination-buff-account", []byte("synthetic-combination-buff-account"))
	if err != nil {
		t.Fatal(err)
	}

	proxyMarker := []byte("https://synthetic-combination:proxy-marker@proxy.invalid:8443")
	nodeOne, err := store.CreateNode(ctx, "combination-node-one", resource.NodeConnectionInput{
		Kind: resource.NodeKindProxy, Region: resource.NodeRegionHongKong,
		EgressMode: resource.EgressModeStatic, ProxyCredential: proxyMarker,
	})
	if err != nil {
		t.Fatal(err)
	}
	nodeTwo, err := store.CreateNode(ctx, "combination-node-two", resource.NodeConnectionInput{
		Kind: resource.NodeKindDirect, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic,
	})
	if err != nil {
		t.Fatal(err)
	}
	spare, err := store.CreateNode(ctx, "combination-spare", resource.NodeConnectionInput{
		Kind: resource.NodeKindDirect, Region: resource.NodeRegionDomestic, EgressMode: resource.EgressModeStatic,
	})
	if err != nil {
		t.Fatal(err)
	}

	type createResult struct {
		combination resource.AccountNodeCombination
		err         error
	}
	start := make(chan struct{})
	results := make(chan createResult, 2)
	for range 2 {
		go func() {
			<-start
			combination, err := store.CreateCombination(ctx, accountOne.ID, nodeOne.ID)
			results <- createResult{combination: combination, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.combination.ID != second.combination.ID {
		t.Fatalf("concurrent duplicate combinations = %+v/%v %+v/%v", first.combination, first.err, second.combination, second.err)
	}
	primary := first.combination
	if primary.Platform != "steam" {
		t.Fatalf("derived combination platform = %q", primary.Platform)
	}
	if duplicate, err := store.CreateCombination(ctx, accountOne.ID, nodeOne.ID); err != nil || duplicate.ID != primary.ID {
		t.Fatalf("idempotent combination = %+v err=%v", duplicate, err)
	}

	pairs := [][2]int64{
		{int64(accountOne.ID), int64(nodeTwo.ID)},
		{int64(accountTwo.ID), int64(nodeOne.ID)},
		{int64(accountTwo.ID), int64(nodeTwo.ID)},
	}
	created := []resource.AccountNodeCombination{primary}
	for _, pair := range pairs {
		combination, err := store.CreateCombination(ctx, resource.AccountID(pair[0]), resource.NodeID(pair[1]))
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, combination)
	}

	buffCombination, err := store.CreateCombination(ctx, buffAccount.ID, nodeOne.ID)
	if err != nil || buffCombination.Platform != "buff" {
		t.Fatalf("same-node buff combination = %+v err=%v", buffCombination, err)
	}
	created = append(created, buffCombination)

	listed, err := store.ListCombinations(ctx)
	if err != nil || len(listed) != len(created) {
		t.Fatalf("listed combinations = %+v err=%v", listed, err)
	}
	for index := 1; index < len(listed); index++ {
		if listed[index-1].ID >= listed[index].ID {
			t.Fatalf("combination order = %+v", listed)
		}
	}
	steamListed, err := store.ListCombinationsFor(ctx, "steam")
	if err != nil || len(steamListed) != 4 {
		t.Fatalf("steam combinations = %+v err=%v", steamListed, err)
	}
	steamResources, err := store.ListCombinationResourcesFor(ctx, "steam")
	if err != nil || len(steamResources) != 4 || steamResources[0].Combination.ID != steamListed[0].ID {
		t.Fatalf("steam combination resources = %+v err=%v", steamResources, err)
	}
	buffListed, err := store.ListCombinationsFor(ctx, "buff")
	if err != nil || len(buffListed) != 1 || buffListed[0].ID != buffCombination.ID {
		t.Fatalf("buff ask combinations = %+v err=%v", buffListed, err)
	}
	steamAgain, err := store.ListCombinationsFor(ctx, "steam")
	if err != nil || len(steamAgain) != 4 {
		t.Fatalf("steam combinations again = %+v err=%v", steamAgain, err)
	}

	joined, found, err := store.CombinationResources(ctx, primary.ID)
	if err != nil || !found || joined.Validate() != nil {
		t.Fatalf("combination resources = %+v found=%v err=%v validate=%v", joined, found, err, joined.Validate())
	}
	if encoded := []byte(fmt.Sprintf("%+v", joined)); bytes.Contains(encoded, proxyMarker) ||
		bytes.Contains(encoded, []byte("synthetic-combination-account-one")) {
		t.Fatal("safe combination resources exposed credential material")
	}
	if !joined.Node.HasProxyCredential {
		t.Fatal("safe combination node lost credential presence")
	}

	if _, err := store.CreateCombination(ctx, accountOne.ID, spare.ID); !errors.Is(err, ErrCombinationIncompatible) {
		t.Fatalf("spare-node combination error = %v", err)
	}
	buffSpare, err := store.CreateCombination(ctx, buffAccount.ID, spare.ID)
	if err != nil {
		t.Fatalf("buff combination on domestic node error = %v", err)
	}
	created = append(created, buffSpare)
	if _, err := store.ReplaceNodeConnection(ctx, nodeTwo.ID, nodeTwo.EgressRevision, resource.NodeConnectionInput{
		Kind: resource.NodeKindDirect, Region: resource.NodeRegionDomestic, EgressMode: resource.EgressModeStatic,
	}); !errors.Is(err, ErrCombinationIncompatible) {
		t.Fatalf("incompatible node replacement error = %v", err)
	}
	if _, err := store.CreateCombination(ctx, resource.AccountID(math.MaxInt64), nodeOne.ID); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("missing account combination error = %v", err)
	}

	if err := store.DeleteAccount(ctx, accountOne.ID); !errors.Is(err, ErrResourceDependency) {
		t.Fatalf("referenced account delete error = %v", err)
	}
	if err := store.DeleteNode(ctx, nodeOne.ID); !errors.Is(err, ErrResourceDependency) {
		t.Fatalf("referenced node delete error = %v", err)
	}

	if err := store.DeleteCombination(ctx, primary.ID); err != nil {
		t.Fatal(err)
	}
	recreated, err := store.CreateCombination(ctx, accountOne.ID, nodeOne.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recreated.ID == primary.ID {
		t.Fatal("deleted combination identity was reused")
	}
	if err := store.DeleteCombination(ctx, primary.ID); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("stale combination delete error = %v", err)
	}
	if read, found, err := store.Combination(ctx, recreated.ID); err != nil || !found || read != recreated {
		t.Fatalf("recreated combination = %+v found=%v err=%v", read, found, err)
	}

	temporary, err := store.CreateAccount(ctx, "steam", "combination-recreated-account", []byte("synthetic-delete-account"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, temporary.ID); err != nil {
		t.Fatal(err)
	}
	recreatedAccount, err := store.CreateAccount(ctx, "steam", "combination-recreated-account", []byte("synthetic-recreated-account"))
	if err != nil || recreatedAccount.ID == temporary.ID {
		t.Fatalf("recreated account = %+v old=%d err=%v", recreatedAccount, temporary.ID, err)
	}
}

func testClosedResourceErrors(t *testing.T, dsn string) {
	db := newTestSchema(t, dsn)
	if err := ApplyMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateAccount(t.Context(), "steam", "closed-read-account", []byte("synthetic-closed-read-account-marker"))
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.CreateNode(t.Context(), "closed-read-node", resource.NodeConnectionInput{
		Kind: resource.NodeKindProxy, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic,
		ProxyCredential: []byte("http://synthetic-closed:read-node-marker@proxy.invalid:8080"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := resourceTime()
	operations := map[string]func() error{
		"create account": func() error {
			_, err := store.CreateAccount(ctx, "steam", "closed-create-account", []byte("synthetic-closed-create-account"))
			return err
		},
		"read account": func() error {
			_, _, err := store.Account(ctx, account.ID)
			return err
		},
		"list accounts": func() error {
			_, err := store.ListAccounts(ctx)
			return err
		},
		"replace account": func() error {
			_, err := store.ReplaceAccountSession(ctx, account.ID, account.SessionRevision, []byte("synthetic-closed-replace-account"))
			return err
		},
		"record account check": func() error {
			_, err := store.RecordAccountSessionCheck(ctx, account.ID, account.SessionRevision, resource.AccountSessionStateValid, now)
			return err
		},
		"open account": func() error {
			_, err := store.OpenAccountSessionAt(ctx, account.ID, account.SessionRevision)
			return err
		},
		"create node": func() error {
			_, err := store.CreateNode(ctx, "closed-create-node", resource.NodeConnectionInput{
				Kind: resource.NodeKindDirect, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic,
			})
			return err
		},
		"read node": func() error {
			_, _, err := store.Node(ctx, node.ID)
			return err
		},
		"list nodes": func() error {
			_, err := store.ListNodes(ctx)
			return err
		},
		"replace node": func() error {
			_, err := store.ReplaceNodeConnection(ctx, node.ID, node.EgressRevision, resource.NodeConnectionInput{
				Kind: resource.NodeKindProxy, Region: resource.NodeRegionForeign, EgressMode: resource.EgressModeStatic,
				ProxyCredential: []byte("http://synthetic-closed:replace-node@proxy.invalid:8080"),
			})
			return err
		},
		"begin node revalidation": func() error {
			_, err := store.BeginNodeRevalidation(ctx, node.ID, node.EgressRevision)
			return err
		},
		"record node exit": func() error {
			_, err := store.RecordNodeExit(ctx, node.ID, node.EgressRevision, netip.MustParseAddr("1.1.1.1"), now, now.Add(time.Hour))
			return err
		},
		"mark node unavailable": func() error {
			_, err := store.MarkNodeUnavailable(ctx, node.ID, node.EgressRevision)
			return err
		},
		"open node": func() error {
			_, err := store.OpenNodeProxyCredentialAt(ctx, node.ID, node.EgressRevision)
			return err
		},
		"create combination": func() error {
			_, err := store.CreateCombination(ctx, account.ID, node.ID)
			return err
		},
		"read combination": func() error {
			_, _, err := store.Combination(ctx, 1)
			return err
		},
		"list combinations": func() error {
			_, err := store.ListCombinations(ctx)
			return err
		},
		"delete combination": func() error { return store.DeleteCombination(ctx, 1) },
		"delete account":     func() error { return store.DeleteAccount(ctx, account.ID) },
		"delete node":        func() error { return store.DeleteNode(ctx, node.ID) },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			err := operation()
			if !errors.Is(err, ErrResourceStorage) || err.Error() != ErrResourceStorage.Error() ||
				strings.Contains(err.Error(), "database is closed") || strings.Contains(err.Error(), "synthetic-closed") {
				t.Fatalf("closed resource operation error = %v", err)
			}
		})
	}
}

func testResourceDatabaseConstraints(t *testing.T, db queryExecer) {
	ctx := t.Context()
	now := resourceTime()
	statements := []struct {
		name string
		sql  string
		args []any
	}{
		{name: "proxy missing plaintext", sql: `INSERT INTO access_nodes(name,kind,region,egress_mode) VALUES ('bad-proxy','proxy','foreign','static')`},
		{name: "available missing revision", sql: `
INSERT INTO access_nodes(name,kind,region,egress_mode,state,exit_address,exit_verified_at,exit_valid_until)
VALUES ('bad-available','direct','foreign','static','available','8.8.8.8',$1,$2)`, args: []any{now, now.Add(time.Hour)}},
		{name: "carrier grade exit", sql: `
INSERT INTO access_nodes(name,kind,region,egress_mode,state,exit_address,exit_verified_revision,exit_verified_at,exit_valid_until)
VALUES ('bad-cgnat','direct','foreign','static','available','100.64.0.1',1,$1,$2)`, args: []any{now, now.Add(time.Hour)}},
		{name: "network prefix exit", sql: `
INSERT INTO access_nodes(name,kind,region,egress_mode,state,exit_address,exit_verified_revision,exit_verified_at,exit_valid_until)
VALUES ('bad-prefix','direct','foreign','static','available','8.8.8.0/24',1,$1,$2)`, args: []any{now, now.Add(time.Hour)}},
		{name: "direct with proxy plaintext", sql: `
INSERT INTO access_nodes(name,kind,region,egress_mode,proxy_plaintext)
VALUES ('bad-direct-secret','direct','foreign','static',$1)`, args: []any{[]byte("synthetic-direct-proxy")}},
		{name: "account control alias", sql: `
INSERT INTO platform_accounts(platform,alias,session_plaintext)
VALUES ('steam',$1,$2)`, args: []any{"bad\nalias", []byte("synthetic-account-session")}},
		{name: "empty session plaintext", sql: `
INSERT INTO platform_accounts(platform,alias,session_plaintext)
VALUES ('steam','empty-session',$1)`, args: []any{[]byte{}}},
	}
	for _, test := range statements {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, test.sql, test.args...); err == nil {
				t.Fatal("invalid direct SQL was accepted")
			}
		})
	}
}

type queryExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func resourceTime() time.Time {
	return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
}

func assertMarkerPlaintext(t *testing.T, db queryExecer, table, column, idColumn string, id int64, marker []byte) {
	t.Helper()
	var stored []byte
	query := "SELECT " + column + " FROM " + table + " WHERE " + idColumn + " = $1"
	if err := db.QueryRowContext(t.Context(), query, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, marker) {
		t.Fatalf("database plaintext = %q, want %q", stored, marker)
	}
}
