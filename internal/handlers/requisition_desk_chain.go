package handlers

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/alvor-technologies/iag-platform-go/approvalchain"
	"github.com/gin-gonic/gin"

	"iag-procurement/backend/internal/middleware"
	"iag-procurement/backend/internal/notifications"
	"iag-procurement/backend/internal/repo"
)

// Desk-chain endpoints. These run beside the tiered approval endpoints of
// migration 018: /approve and /reject collect amount-band signatures, while
// these walk a requisition through named desks. A requisition uses one or the
// other — the repo refuses to open a desk chain on a requisition that already
// carries tier signatures.

// deskActor builds the engine actor from the verified token. Roles come from
// the platform claims, so a desk is held by whoever the identity service says
// holds it rather than by anything procurement stores.
func deskActor(c *gin.Context) approvalchain.Actor {
	a := approvalchain.Actor{ID: authActorEmail(c)}
	if claims, ok := middleware.PlatformClaims(c); ok && claims != nil {
		a.Roles = append(a.Roles, claims.Roles...)
		if len(claims.Roles) == 0 {
			a.Roles = append(a.Roles, claims.Groups...)
		}
	}
	if v, ok := c.Get(middleware.CtxSuper); ok {
		super, _ := v.(bool)
		a.Admin = super
	}
	// Each desk declares a RequiredPerm in the matrix; this is how the engine
	// checks it. Holding the desk by role is not enough on its own — the same
	// rule the tiered path applies to its bands.
	a.HasPerm = func(code string) bool { return middleware.HasPerm(c, code) }
	// Attrs would carry the actor's own scope values — a department, a cost
	// centre — for desks scoped to a group rather than a person. None are
	// supplied yet: the platform token carries email, groups, roles and
	// permissions, and nothing that identifies which department the holder
	// belongs to. Until the token does, a department-scoped desk falls back to
	// matching on role, which the engine reports through TrackerStep.ScopedTo
	// so the fallback is visible rather than silent.
	//
	// The desk the default matrix actually scopes — the PM desk, by
	// project_owner — matches on the actor's identity and needs no attribute.
	return a
}

type deskOpenBody struct {
	// Chain selects the chain: "requisition", "material.stores" or
	// "material.procurement". Empty defaults to "requisition".
	Chain string `json:"chain"`
	// Skip names desks that do not apply to this request — how a fleet or
	// general request skips the PM desk without a second chain definition.
	Skip []string `json:"skip"`
}

type deskReasonBody struct {
	Reason string `json:"reason"`
	Note   string `json:"note"`
}

func bindDeskBody(c *gin.Context, body any) bool {
	if err := c.ShouldBindJSON(body); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return false
	}
	return true
}

func (a *API) deskReady(c *gin.Context) bool {
	if a.procurement == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "procurement writes not configured"})
		return false
	}
	return true
}

// listApprovalChains returns the desk matrix so a client can render trackers and
// desk filters without hardcoding a stage list.
func (a *API) listApprovalChains(c *gin.Context) {
	if !a.deskReady(c) {
		return
	}
	eng, err := a.procurement.DeskEngine(c.Request.Context())
	if mapProcurementErr(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"chains": eng.Registry().Meta()})
}

// listDeskMatrix returns the editable matrix rows — who holds each desk, in
// what order, and from what amount it engages.
func (a *API) listDeskMatrix(c *gin.Context) {
	if !a.deskReady(c) {
		return
	}
	rows, err := a.procurement.ListDeskMatrix(c.Request.Context())
	if mapProcurementErr(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"desks": rows})
}

// putDeskChain rewrites one chain's desks. Approvers are configuration: this is
// how a deployment adds a Department Head desk, moves an escalation band, or
// points a desk at a different role — without a code change or a deploy.
func (a *API) putDeskChain(c *gin.Context) {
	if !a.deskReady(c) {
		return
	}
	var body struct {
		Desks []repo.DeskRow `json:"desks" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rows, err := a.procurement.ReplaceDeskChain(
		c.Request.Context(), strings.TrimSpace(c.Param("chain")), body.Desks)
	if mapProcurementErr(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"desks": rows})
}

// deleteDeskChain removes a chain that no open request is using.
func (a *API) deleteDeskChain(c *gin.Context) {
	if !a.deskReady(c) {
		return
	}
	if err := a.procurement.DeleteDeskChain(
		c.Request.Context(), strings.TrimSpace(c.Param("chain"))); mapProcurementErr(c, err) {
		return
	}
	c.Status(http.StatusNoContent)
}

// reloadDeskChains re-reads the matrix into the running process, so a matrix
// edited directly in SQL takes effect without a restart.
func (a *API) reloadDeskChains(c *gin.Context) {
	if !a.deskReady(c) {
		return
	}
	eng, err := a.procurement.ReloadDeskChains(c.Request.Context())
	if mapProcurementErr(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"chains": eng.Registry().Meta()})
}

// approvalDesk is the queue: every requisition waiting on a desk the caller
// holds. This is the endpoint the whole model exists for — one place a GM or an
// Accounts Assistant sees what is theirs to act on.
func (a *API) approvalDesk(c *gin.Context) {
	if !a.deskReady(c) {
		return
	}
	items, err := a.procurement.DeskQueue(c.Request.Context(), deskActor(c))
	if mapProcurementErr(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "count": len(items)})
}

// getRequisitionDeskProgress renders one requisition's tracker: every desk,
// including the ones its amount skipped.
func (a *API) getRequisitionDeskProgress(c *gin.Context) {
	if !a.deskReady(c) {
		return
	}
	state, err := a.procurement.DeskProgress(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if mapProcurementErr(c, err) {
		return
	}
	c.JSON(http.StatusOK, state)
}

// openRequisitionDeskChain puts a requisition onto a desk chain in draft.
func (a *API) openRequisitionDeskChain(c *gin.Context) {
	if !a.deskReady(c) {
		return
	}
	var body deskOpenBody
	if !bindDeskBody(c, &body) {
		return
	}
	chain := strings.TrimSpace(body.Chain)
	if chain == "" {
		chain = "requisition"
	}
	skip := make([]approvalchain.DeskKey, 0, len(body.Skip))
	for _, s := range body.Skip {
		if s = strings.TrimSpace(s); s != "" {
			skip = append(skip, approvalchain.DeskKey(s))
		}
	}

	state, err := a.procurement.OpenDeskChain(
		c.Request.Context(), strings.TrimSpace(c.Param("id")), chain, deskActor(c).ID, skip)
	if mapProcurementErr(c, err) {
		return
	}
	c.JSON(http.StatusCreated, gin.H{"state": state})
}

// submitRequisitionToDesk moves a draft — or an amended request — onto its first
// engaged desk.
func (a *API) submitRequisitionToDesk(c *gin.Context) {
	a.applyDeskTransition(c, func(eng *approvalchain.Engine, s approvalchain.State, actor approvalchain.Actor, body deskReasonBody) (approvalchain.State, error) {
		return eng.Submit(s, actor)
	})
}

// advanceRequisitionDesk passes the request to the next engaged desk.
func (a *API) advanceRequisitionDesk(c *gin.Context) {
	a.applyDeskTransition(c, func(eng *approvalchain.Engine, s approvalchain.State, actor approvalchain.Actor, body deskReasonBody) (approvalchain.State, error) {
		return eng.Advance(s, actor, body.Note)
	})
}

// rejectRequisitionDesk refuses the request. The reason is mandatory — the
// engine rejects a blank one with a 400, and the database constraint refuses it
// too.
func (a *API) rejectRequisitionDesk(c *gin.Context) {
	a.applyDeskTransition(c, func(eng *approvalchain.Engine, s approvalchain.State, actor approvalchain.Actor, body deskReasonBody) (approvalchain.State, error) {
		return eng.Reject(s, actor, body.Reason)
	})
}

// amendRequisitionDesk returns the request to its requester, with a reason, and
// resets the walk so the corrected version is approved from the first desk again.
func (a *API) amendRequisitionDesk(c *gin.Context) {
	a.applyDeskTransition(c, func(eng *approvalchain.Engine, s approvalchain.State, actor approvalchain.Actor, body deskReasonBody) (approvalchain.State, error) {
		return eng.Amend(s, actor, body.Reason)
	})
}

// cancelRequisitionDesk withdraws the request. Requester or admin only.
func (a *API) cancelRequisitionDesk(c *gin.Context) {
	a.applyDeskTransition(c, func(eng *approvalchain.Engine, s approvalchain.State, actor approvalchain.Actor, body deskReasonBody) (approvalchain.State, error) {
		return eng.Cancel(s, actor, body.Reason)
	})
}

// reopenRequisitionDesk puts a rejected request back on the desk that refused
// it. Admin only, recorded as an override.
func (a *API) reopenRequisitionDesk(c *gin.Context) {
	a.applyDeskTransition(c, func(eng *approvalchain.Engine, s approvalchain.State, actor approvalchain.Actor, body deskReasonBody) (approvalchain.State, error) {
		return eng.Reopen(s, actor, body.Reason)
	})
}

type deskTransitionFn func(*approvalchain.Engine, approvalchain.State, approvalchain.Actor, deskReasonBody) (approvalchain.State, error)

// applyDeskTransition is the shared body of every desk action: bind, resolve the
// actor, apply the transition under a row lock, and return the refreshed
// tracker so the client never has to re-fetch to redraw.
func (a *API) applyDeskTransition(c *gin.Context, fn deskTransitionFn) {
	if !a.deskReady(c) {
		return
	}
	var body deskReasonBody
	if !bindDeskBody(c, &body) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	actor := deskActor(c)

	// The outcome event is enqueued to the transactional outbox inside
	// ApplyDeskTransition, not emitted here: an approval that committed must
	// not be able to lose its event because the process died afterwards.
	if _, err := a.procurement.ApplyDeskTransition(c.Request.Context(), id,
		func(eng *approvalchain.Engine, s approvalchain.State) (approvalchain.State, error) {
			return fn(eng, s, actor, body)
		}); mapProcurementErr(c, err) {
		return
	}
	a.InvalidateSeedCache(c.Request.Context())

	progress, err := a.procurement.DeskProgress(c.Request.Context(), id)
	if mapProcurementErr(c, err) {
		return
	}
	a.notifyDeskTransitionAsync(c, id, progress)
	c.JSON(http.StatusOK, progress)
}

// notifyDeskTransitionAsync runs the desk notification without holding the
// response.
//
// The context is deliberately detached from the request. Gin cancels
// c.Request.Context() as soon as the response is written, so handing it to a
// goroutine would cancel the dispatch the instant it became useful — the
// approval would land, the response would return, and the notification would
// silently never go out. That failure looks exactly like "approvals sit for a
// week", which is the problem desk chains exist to solve, so it is worth the
// explicit WithoutCancel rather than a background context: the trace and
// request-id baggage survive, the cancellation does not.
//
// The timeout is its own, generous compared to the request but bounded, so a
// hung notifications service leaks one goroutine for at most that long instead
// of forever.
func (a *API) notifyDeskTransitionAsync(c *gin.Context, requisitionID string, st *repo.DeskState) {
	if a.notify == nil || st == nil {
		return
	}
	ctx := context.WithoutCancel(c.Request.Context())
	go func() {
		ctx, cancel := context.WithTimeout(ctx, deskNotifyTimeout)
		defer cancel()
		a.notifyDeskTransition(ctx, requisitionID, st)
	}()
}

// deskNotifyTimeout bounds the detached dispatch above.
const deskNotifyTimeout = 30 * time.Second

// notifyDeskTransition tells whoever the request now waits on that it is theirs.
//
// A desk queue nobody is told about is a queue nobody watches, which is how
// approvals sit for a week. On an advance the incoming desk's holders are
// alerted; on a rejection or an amendment the requester is told, with the
// reason, because that is the person who has to act next.
//
// This is best-effort and deliberately after the commit: the queue is the
// durable source of truth, so a lost email delays a nudge, never an approval.
// A failed dispatch must not roll back an approval that already happened.
//
// It is also deliberately off the response path — see notifyDeskTransitionAsync,
// which is what callers use. Running it inline made the approver wait for a
// desk lookup plus an outbound HTTP call per recipient before their click
// returned.
func (a *API) notifyDeskTransition(ctx context.Context, requisitionID string, st *repo.DeskState) {
	if a.notify == nil || st == nil {
		return
	}
	title, message, to := "", "", []string(nil)

	switch st.State.Status {
	case approvalchain.StatusInFlight:
		recipients, err := a.procurement.DeskRecipients(
			ctx, st.State.ChainKey, st.State.Desk, st.State.Requester)
		if err != nil || len(recipients) == 0 {
			return
		}
		to = recipients
		title = "Requisition awaiting " + st.Progress.WaitingOn
		message = requisitionID + " is on your desk: " + st.Progress.ActionLabel + "."
	case approvalchain.StatusReturned:
		to = []string{st.State.Requester}
		title = "Requisition returned for amendment"
		message = requisitionID + " was returned: " + st.State.LastReason()
	case approvalchain.StatusRejected:
		to = []string{st.State.Requester}
		title = "Requisition rejected"
		message = requisitionID + " was rejected: " + st.State.LastReason()
	case approvalchain.StatusApproved:
		to = []string{st.State.Requester}
		title = "Requisition " + strings.ToLower(st.Progress.StatusLabel)
		message = requisitionID + " cleared every approval desk."
	default:
		return
	}

	to = nonEmpty(to)
	if len(to) == 0 {
		return
	}
	if err := a.notify.EnqueueAlertEmail(ctx, notifications.AlertJobPayload{
		To:      to,
		Title:   title,
		Message: message,
		Detail:  st.Progress.ChainLabel,
	}); err != nil {
		log.Printf("desk notification for %s: %v", requisitionID, err)
	}
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
