package server

// System-update push endpoints (0.3.2 updates-modal data plane; row shape
// and the four-endpoint split are the 0922 contract pinned with the modal
// shell in app.js updatesModalInit — latest returns {unread, push,
// unread_more}, read acks by id, admin list/upsert curate content; drafts
// never surface on self endpoints, latest failure = the shell stays silent).
//
//   GET  /api/updates/latest  (auth account) -> {"unread","push","unread_more"}
//   POST /api/updates/read    (auth account) {"id"} -> {"ok","last_read_push_id"}
//   GET  /api/admin/updates   (admin) -> {"pushes":[...]} (drafts included)
//   PUT  /api/admin/updates   (admin) {"id"?,"version","title","body_md","published"} -> {"ok","push"}

import (
	"log"
	"net/http"

	"github.com/lieuyimo/mailofagents/internal/store"
)

// The upstream seed shipped before the independent version line existed.
// Keep its exact copy so untouched installations can be migrated in place.
const (
	legacyPushVersion = "v0.3.1"
	legacyPushTitle   = "Mail of Agents v0.3.1"
	legacyPushBody    = "· 优化连接图渲染逻辑及控件显示\n· worker: 终端窗口缩放即时重绘，不再错位花屏；增加鼠标交互控件\n· worker 心跳状态接入概览"
)

// defaultPushSeed is the first-boot announcement for this repository. The
// middle dot + space lines are the modal's hanging-indent item syntax.
func defaultPushSeed() []store.PushRecord {
	return []store.PushRecord{{
		Version:   "v0.1.0",
		Title:     "Mail of Agents v0.1.0",
		BodyMD:    "· 独立维护版本：版本线从 v0.1.0 重新开始\n· 修复同毫秒 ULID 排序与 push ID 覆盖，列表与分页稳定\n· gateway 认证边界测试 + CI（build 与 5 个 race 分片）",
		Published: true,
	}}
}

// migrateLegacyPushSeed replaces the untouched upstream seed that existing
// installations still carry. Anything else — admin-curated copy, multiple
// pushes, drafts-only — is left alone.
func migrateLegacyPushSeed(st *store.Store) {
	pushes, err := st.ListPushes(true)
	if err != nil || len(pushes) != 1 {
		return
	}
	old := pushes[0]
	if old.Version != legacyPushVersion || old.Title != legacyPushTitle || old.BodyMD != legacyPushBody {
		return
	}
	seed := defaultPushSeed()[0]
	seed.ID = old.ID
	seed.Published = old.Published
	seed.PublishedAt = old.PublishedAt
	if _, err := st.UpsertPush(seed); err != nil {
		log.Printf("updates: migrate legacy seed: %v", err)
	}
}

// handleUpdatesLatest answers the modal's once-per-overview-entry query.
func (s *Server) handleUpdatesLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	who := accountFrom(r.Context())
	latest, ok, err := s.store.LatestPublishedPush()
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "no pushes", http.StatusNotFound)
		return
	}
	last, err := s.store.LastReadPushID(who)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	unread := latest.ID > last
	unreadMore := 0
	if unread {
		if n, err := s.store.CountPublishedAfter(last); err == nil && n > 1 {
			unreadMore = n - 1 // the shown latest is not counted as "more"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"unread":      unread,
		"push":        latest,
		"unread_more": unreadMore,
	})
}

// handleUpdatesRead acknowledges the shown push. The watermark is
// monotonic, so a late ack of an older id never resurrects unread state.
func (s *Server) handleUpdatesRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	who := accountFrom(r.Context())
	var body struct {
		ID *int64 `json:"id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		badRequest(w, "invalid body: "+err.Error())
		return
	}
	if body.ID == nil || *body.ID <= 0 {
		badRequest(w, "id required")
		return
	}
	p, found, err := s.store.GetPush(*body.ID)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	if !found || !p.Published {
		http.Error(w, "no such push", http.StatusNotFound)
		return
	}
	if err := s.store.MarkPushRead(who, p.ID); err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	last, _ := s.store.LastReadPushID(who)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "last_read_push_id": last})
}

// handleAdminUpdates lists (GET) or upserts (PUT) pushes; drafts included in
// the list so unpublished content survives server restarts.
func (s *Server) handleAdminUpdates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		pushes, err := s.store.ListPushes(true)
		if err != nil {
			http.Error(w, "store error", http.StatusInternalServerError)
			return
		}
		if pushes == nil {
			pushes = []store.PushRecord{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"pushes": pushes})

	case http.MethodPut:
		var body struct {
			ID        *int64 `json:"id"`
			Version   string `json:"version"`
			Title     string `json:"title"`
			BodyMD    string `json:"body_md"`
			Published *bool  `json:"published"`
		}
		if err := decodeJSON(r, &body); err != nil {
			badRequest(w, "invalid body: "+err.Error())
			return
		}
		p := store.PushRecord{
			Version:   body.Version,
			Title:     body.Title,
			BodyMD:    body.BodyMD,
			Published: body.Published != nil && *body.Published,
		}
		if body.ID != nil {
			cur, found, err := s.store.GetPush(*body.ID)
			if err != nil {
				http.Error(w, "store error", http.StatusInternalServerError)
				return
			}
			if !found {
				http.Error(w, "no such push", http.StatusNotFound)
				return
			}
			p.ID = cur.ID
			if p.PublishedAt == 0 {
				p.PublishedAt = cur.PublishedAt // editing must not restamp first-publish
			}
			if body.Title == "" {
				p.Title = cur.Title
			}
			if body.BodyMD == "" {
				p.BodyMD = cur.BodyMD
			}
			if body.Version == "" {
				p.Version = cur.Version
			}
		}
		saved, err := s.store.UpsertPush(p)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "push": saved})

	default:
		methodNotAllowed(w)
	}
}
