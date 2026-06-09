package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/dnikolayev/coderoam/internal/types"
)

func TestRecordIncomingMessageDeduplicates(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msg := types.IncomingMessage{
		ID:        "msg-1",
		ChatID:    "chat@g.us",
		SenderID:  "sender@s.whatsapp.net",
		Text:      "secret text",
		RawText:   "@bridge secret text",
		Timestamp: time.Now(),
	}
	first, fresh, err := store.RecordIncomingMessage(t.Context(), "test", msg, false)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("first insert was not fresh")
	}
	if first.Text != "" {
		t.Fatalf("stored text = %q", first.Text)
	}
	second, fresh, err := store.RecordIncomingMessage(t.Context(), "test", msg, false)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("duplicate insert was fresh")
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate id = %d, want %d", second.ID, first.ID)
	}
}

func TestMarkMessageProcessingClaimsReceivedOnce(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msg := types.IncomingMessage{
		ID:        "msg-1",
		ChatID:    "chat@g.us",
		SenderID:  "sender@s.whatsapp.net",
		Text:      "hello",
		RawText:   "hello",
		Timestamp: time.Now(),
	}
	record, _, err := store.RecordIncomingMessage(t.Context(), "test", msg, true)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.MarkMessageProcessing(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("expected first processing claim to succeed")
	}
	claimed, err = store.MarkMessageProcessing(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("expected second processing claim to fail")
	}
}

func TestPendingIncomingMessagesReturnsReceivedRows(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msg := types.IncomingMessage{
		ID:        "msg-1",
		ChatID:    "chat@g.us",
		ChatType:  types.ChatTypeGroup,
		SenderID:  "sender@s.whatsapp.net",
		Text:      "hello",
		RawText:   "hello",
		Timestamp: time.Now(),
	}
	record, _, err := store.RecordIncomingMessage(t.Context(), "test", msg, true)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingIncomingMessages(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending count = %d, want 1", len(pending))
	}
	if pending[0].ID != msg.ID || pending[0].ChatID != msg.ChatID || pending[0].Text != msg.Text {
		t.Fatalf("pending message = %+v", pending[0])
	}
	if err := store.MarkMessageProcessed(t.Context(), record.ID, "processed"); err != nil {
		t.Fatal(err)
	}
	pending, err = store.PendingIncomingMessages(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending count after processed = %d, want 0", len(pending))
	}
}

func TestActiveInboxDedupesClaimsAndMarksDone(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msg := types.IncomingMessage{
		ID:         "wa-1",
		ChatID:     "chat@g.us",
		SenderID:   "sender@s.whatsapp.net",
		SenderName: "Nick",
		Text:       "hello from whatsapp",
		RawText:    "hello from whatsapp",
		Media: []types.MediaAttachment{{
			Type:            "voice",
			MIMEType:        "audio/ogg; codecs=opus",
			Size:            777,
			DurationSeconds: 3,
			LocalPath:       filepath.Join(t.TempDir(), "voice.ogg"),
			Transcript:      "hello from voice",
		}},
		Timestamp: time.Now(),
	}
	first, fresh, err := store.StoreActiveInboxMessage(t.Context(), "test", "codex-session", "codex-session", msg)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("first active inbox insert was not fresh")
	}
	if first.SessionID != "codex-session" {
		t.Fatalf("first session = %q, want codex-session", first.SessionID)
	}
	second, fresh, err := store.StoreActiveInboxMessage(t.Context(), "test", "codex-session", "codex-session", msg)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("duplicate active inbox insert was fresh")
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate active inbox id = %d, want %d", second.ID, first.ID)
	}
	claimed, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), "test", "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected unread active inbox row")
	}
	if claimed.ID != first.ID || claimed.Status != "claimed" {
		t.Fatalf("claimed = %+v", claimed)
	}
	if claimed.SessionID != "codex-session" || claimed.ClaimedBySessionID != "codex-session" {
		t.Fatalf("claimed session fields = %+v", claimed)
	}
	if len(claimed.Media) != 1 || claimed.Media[0].Type != "voice" || claimed.Media[0].LocalPath == "" || claimed.Media[0].Transcript != "hello from voice" {
		t.Fatalf("claimed media = %+v", claimed.Media)
	}
	receipts, err := store.PendingActiveReadReceipts(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 {
		t.Fatalf("pending read receipts = %d, want 1", len(receipts))
	}
	if receipts[0].ExternalMessageID != msg.ID || receipts[0].ChatID != msg.ChatID || receipts[0].SenderID != msg.SenderID {
		t.Fatalf("read receipt = %+v", receipts[0])
	}
	_, ok, err = store.ClaimNextActiveInboxForSession(t.Context(), "test", "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no second unread active inbox row")
	}
	if err := store.MarkActiveInboxDone(t.Context(), "test", claimed.ID, "done"); err != nil {
		t.Fatal(err)
	}
	counts, err := store.ActiveInboxCounts(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if counts["done"] != 1 {
		t.Fatalf("done count = %d, want 1", counts["done"])
	}
}

func TestActiveInboxRequeueClaimed(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msg := types.IncomingMessage{
		ID:        "wa-requeue",
		ChatID:    "chat@g.us",
		SenderID:  "sender@s.whatsapp.net",
		Text:      "recover me",
		Timestamp: time.Now(),
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), "test", "codex-session", "codex-session", msg); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), "test", "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected claimed row")
	}
	requeued, err := store.RequeueActiveInbox(t.Context(), "test", claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !requeued {
		t.Fatal("expected claimed row to be requeued")
	}
	unread, err := store.ListActiveInbox(t.Context(), "test", "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].ID != claimed.ID || unread[0].ClaimedBySessionID != "" || unread[0].ClaimedAt != nil {
		t.Fatalf("unread after requeue = %+v", unread)
	}
}

func TestListClaimedActiveInboxForSession(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, tc := range []struct {
		id      string
		alias   string
		session string
		text    string
	}{
		{id: "wa-codex", alias: "codex-session", session: "codex-session", text: "claimed for codex"},
		{id: "wa-other", alias: "other-session", session: "other-session", text: "claimed for other"},
	} {
		msg := types.IncomingMessage{
			ID:        tc.id,
			ChatID:    tc.alias + "@g.us",
			SenderID:  "sender@s.whatsapp.net",
			Text:      tc.text,
			Timestamp: time.Now(),
		}
		if _, _, err := store.StoreActiveInboxMessage(t.Context(), "test", tc.alias, tc.session, msg); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), "test", tc.session); err != nil {
			t.Fatal(err)
		} else if !ok {
			t.Fatalf("expected claimed row for %s", tc.session)
		}
	}
	claimed, err := store.ListClaimedActiveInboxForSession(t.Context(), "test", "codex-session", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ExternalMessageID != "wa-codex" {
		t.Fatalf("claimed = %+v", claimed)
	}
}

func TestActiveInboxRecoversAbandonedClaims(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msg := types.IncomingMessage{
		ID:        "wa-abandoned",
		ChatID:    "chat@g.us",
		SenderID:  "sender@s.whatsapp.net",
		Text:      "recover after restart",
		Timestamp: time.Now(),
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), "test", "codex-session", "codex-session", msg); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), "test", "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected claimed row")
	}
	oldClaim := time.Now().Add(-time.Minute)
	if _, err := store.db.ExecContext(t.Context(), `UPDATE active_inbox SET claimed_at = ? WHERE id = ?`, formatTime(oldClaim), claimed.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.RecoverAbandonedActiveInbox(t.Context(), "test", "codex-session", 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	unread, err := store.ListActiveInbox(t.Context(), "test", "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].ID != claimed.ID || unread[0].ClaimedAt != nil || unread[0].ClaimedBySessionID != "" {
		t.Fatalf("unread after recovery = %+v", unread)
	}
}

func TestActiveInboxRecoveryKeepsLiveWatcherClaims(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msg := types.IncomingMessage{
		ID:        "wa-live-watcher",
		ChatID:    "chat@g.us",
		SenderID:  "sender@s.whatsapp.net",
		Text:      "do not recover",
		Timestamp: time.Now(),
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), "test", "codex-session", "codex-session", msg); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), "test", "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected claimed row")
	}
	oldClaim := time.Now().Add(-time.Minute)
	if _, err := store.db.ExecContext(t.Context(), `UPDATE active_inbox SET claimed_at = ? WHERE id = ?`, formatTime(oldClaim), claimed.ID); err != nil {
		t.Fatal(err)
	}
	if _, acquired, err := store.AcquireActiveWatcher(t.Context(), "test", "codex-session", "host:111", 111, 15*time.Second, false); err != nil {
		t.Fatal(err)
	} else if !acquired {
		t.Fatal("expected watcher lock")
	}
	recovered, err := store.RecoverAbandonedActiveInbox(t.Context(), "test", "codex-session", 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 0 {
		t.Fatalf("recovered = %d, want 0", recovered)
	}
	claimedRows, err := store.ListActiveInbox(t.Context(), "test", "claimed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimedRows) != 1 || claimedRows[0].ID != claimed.ID {
		t.Fatalf("claimed after recovery = %+v", claimedRows)
	}
}

func TestActiveInboxClaimsOnlyMatchingSession(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msgA := types.IncomingMessage{
		ID:        "wa-a",
		ChatID:    "chat-a@g.us",
		SenderID:  "sender@s.whatsapp.net",
		Text:      "for session a",
		Timestamp: time.Now(),
	}
	msgB := types.IncomingMessage{
		ID:        "wa-b",
		ChatID:    "chat-b@g.us",
		SenderID:  "sender@s.whatsapp.net",
		Text:      "for session b",
		Timestamp: time.Now(),
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), "test", "alias-a", "session-a", msgA); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), "test", "alias-b", "session-b", msgB); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), "test", "session-b")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a session-b row")
	}
	if claimed.ExternalMessageID != msgB.ID || claimed.SessionID != "session-b" || claimed.ClaimedBySessionID != "session-b" {
		t.Fatalf("claimed = %+v", claimed)
	}
	_, ok, err = store.ClaimNextActiveInboxForSession(t.Context(), "test", "session-b")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("session-b claimed a second row")
	}
	claimed, ok, err = store.ClaimNextActiveInboxForSession(t.Context(), "test", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || claimed.ExternalMessageID != msgA.ID {
		t.Fatalf("session-a claim = %+v ok=%t", claimed, ok)
	}
}

func TestActiveInboxBatchClaimKeepsSameSessionInsideChat(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	messages := []types.IncomingMessage{
		{
			ID:        "wa-a-1",
			ChatID:    "chat-a@g.us",
			SenderID:  "sender@s.whatsapp.net",
			Text:      "first same chat",
			Timestamp: now,
		},
		{
			ID:        "wa-b-1",
			ChatID:    "chat-b@g.us",
			SenderID:  "sender@s.whatsapp.net",
			Text:      "same session other chat",
			Timestamp: now.Add(time.Second),
		},
		{
			ID:        "wa-a-2",
			ChatID:    "chat-a@g.us",
			SenderID:  "sender@s.whatsapp.net",
			Text:      "second same chat",
			Timestamp: now.Add(2 * time.Second),
		},
	}
	for _, msg := range messages {
		if _, _, err := store.StoreActiveInboxMessage(t.Context(), "test", "codex-session", "codex-session", msg); err != nil {
			t.Fatal(err)
		}
	}

	claimed, err := store.ClaimActiveInboxBatchForSession(t.Context(), "test", "chat-a@g.us", "codex-session", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed count = %d, want 2: %+v", len(claimed), claimed)
	}
	for _, record := range claimed {
		if record.ChatID != "chat-a@g.us" || record.SessionID != "codex-session" || record.ClaimedBySessionID != "codex-session" {
			t.Fatalf("claimed wrong row = %+v", record)
		}
	}
	unread, err := store.ListActiveInbox(t.Context(), "test", "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unread) != 1 || unread[0].ExternalMessageID != "wa-b-1" || unread[0].ChatID != "chat-b@g.us" {
		t.Fatalf("remaining unread = %+v", unread)
	}
}

func TestActiveWatcherExclusiveLock(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	first, acquired, err := store.AcquireActiveWatcher(t.Context(), "test", "codex-session", "host:111", 111, 15*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired || first.ConsumerID != "host:111" || first.Status != "active" {
		t.Fatalf("first watcher = %+v acquired=%t", first, acquired)
	}

	existing, acquired, err := store.AcquireActiveWatcher(t.Context(), "test", "codex-session", "host:222", 222, 15*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Fatal("second fresh watcher acquired lock")
	}
	if existing.ConsumerID != "host:111" || existing.PID != 111 {
		t.Fatalf("existing watcher = %+v", existing)
	}

	alive, err := store.HeartbeatActiveWatcher(t.Context(), "test", "codex-session", "host:111")
	if err != nil {
		t.Fatal(err)
	}
	if !alive {
		t.Fatal("heartbeat did not update active watcher")
	}

	stale := time.Now().Add(-time.Minute)
	if _, err := store.db.ExecContext(t.Context(), `UPDATE active_watchers SET heartbeat_at = ? WHERE profile_id = ? AND session_id = ?`, formatTime(stale), "test", "codex-session"); err != nil {
		t.Fatal(err)
	}
	expired, err := store.ExpireActiveWatchers(t.Context(), "test", 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if expired != 1 {
		t.Fatalf("expired watchers = %d, want 1", expired)
	}
	staleRecord, err := store.GetActiveWatcher(t.Context(), "test", "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if staleRecord.Status != "stale" {
		t.Fatalf("stale status = %q, want stale", staleRecord.Status)
	}
	replacement, acquired, err := store.AcquireActiveWatcher(t.Context(), "test", "codex-session", "host:222", 222, 15*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired || replacement.ConsumerID != "host:222" {
		t.Fatalf("stale replacement = %+v acquired=%t", replacement, acquired)
	}

	taken, acquired, err := store.AcquireActiveWatcher(t.Context(), "test", "codex-session", "host:333", 333, 15*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired || taken.ConsumerID != "host:333" {
		t.Fatalf("takeover = %+v acquired=%t", taken, acquired)
	}
	if err := store.ReleaseActiveWatcher(t.Context(), "test", "codex-session", "host:333"); err != nil {
		t.Fatal(err)
	}
	released, err := store.GetActiveWatcher(t.Context(), "test", "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != "stopped" {
		t.Fatalf("released status = %q, want stopped", released.Status)
	}
}

func TestActiveReadReceiptMarksSent(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msg := types.IncomingMessage{
		ID:        "wa-read",
		ChatID:    "chat@g.us",
		SenderID:  "sender@s.whatsapp.net",
		Text:      "hello",
		Timestamp: time.Now(),
	}
	if _, _, err := store.StoreActiveInboxMessage(t.Context(), "test", "codex-session", "codex-session", msg); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimNextActiveInboxForSession(t.Context(), "test", "codex-session"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("expected claimed row")
	}
	receipts, err := store.PendingActiveReadReceipts(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 {
		t.Fatalf("pending read receipts = %d, want 1", len(receipts))
	}
	if err := store.MarkActiveReadReceiptSent(t.Context(), receipts[0].ID); err != nil {
		t.Fatal(err)
	}
	count, err := store.ActiveReadReceiptPendingCount(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("pending receipt count = %d, want 0", count)
	}
}

func TestActiveOutboxQueuesAndMarksSent(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id, err := store.QueueActiveOutbox(t.Context(), "test", "chat@g.us", "important update", true)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingActiveOutbox(t.Context(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != id || !pending[0].Important {
		t.Fatalf("pending outbox = %+v", pending)
	}
	if err := store.MarkActiveOutboxSent(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	count, err := store.ActiveOutboxPendingCount(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("pending count = %d, want 0", count)
	}
}

func TestMigrateMessagesToActiveInbox(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "bridge.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	msg := types.IncomingMessage{
		ID:        "wa-legacy",
		ChatID:    "chat@g.us",
		SenderID:  "sender@s.whatsapp.net",
		Text:      "legacy",
		RawText:   "legacy",
		Timestamp: time.Now(),
	}
	record, _, err := store.RecordIncomingMessage(t.Context(), "test", msg, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkMessageProcessing(t.Context(), record.ID); err != nil {
		t.Fatal(err)
	}
	migrated, err := store.MigrateMessagesToActiveInbox(t.Context(), "test", "chat@g.us", "codex-session", "codex-session")
	if err != nil {
		t.Fatal(err)
	}
	if migrated != 1 {
		t.Fatalf("migrated = %d, want 1", migrated)
	}
	pending, err := store.ListActiveInbox(t.Context(), "test", "unread", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ExternalMessageID != msg.ID || pending[0].SessionID != "codex-session" {
		t.Fatalf("pending = %+v", pending)
	}
	legacy, err := store.GetMessageByExternalID(t.Context(), "test", msg.ID, "incoming")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Status != "active_inbox" {
		t.Fatalf("legacy status = %q, want active_inbox", legacy.Status)
	}
}
