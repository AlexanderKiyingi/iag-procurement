-- Commitment and disbursement are different events with different authorizing
-- officers, and the platform already models both:
--
--   commitment   — this chain. Approving it takes the budget row FOR UPDATE and
--                  encumbers it. The authorizing officer is the budget holder,
--                  which for project spend is the project manager.
--   disbursement — finance_approvals in iag-finance, keyed target_type='payment'
--                  with amount-band tiers, gated upstream by the three-way match
--                  (ApproveInvoice refuses an invoice whose match has not
--                  resolved, and a receipt against an unapproved PO is refused).
--
-- The money desk at the end of this chain was a third approval that authorized
-- neither. 021 renamed it from "Make payment"/"Paid" to "Authorize payment",
-- which made the label honest but left it asking a finance officer to authorize
-- a payment before a purchase order exists, before anything is received, before
-- there is an invoice and before any match has run — after which finance
-- authorizes the same payment again, properly, against a matched invoice.
--
-- No code referenced either desk. They are removed rather than renamed: the
-- commitment chain now ends when the commitment is authorized.

DELETE FROM requisition_approval_desks
 WHERE (chain_key, desk) IN (('requisition', 'finance'), ('material.procurement', 'finance_pay'));

-- The terminal was derived from whichever desk sat last, so with the money desk
-- gone a requisition would finish as "Accounts Assistant Approved", "GM Approved"
-- or "CEO Approved" purely according to its value. All three mean the same thing
-- — authorized, ready to raise a purchase order — and reading them as one state
-- would mean knowing all three strings.
--
-- The chain now carries its own terminal. Who signed is not lost by this: every
-- desk approval keeps its own row in requisition_approval_steps with actor,
-- role and timestamp. The terminal says where the request got to; the history
-- says who took it there.
--
-- Chain-level value carried on every row of the chain, following
-- no_repeat_approver. Any non-empty row wins.
ALTER TABLE requisition_approval_desks
    ADD COLUMN IF NOT EXISTS terminal_label TEXT NOT NULL DEFAULT '';

UPDATE requisition_approval_desks SET terminal_label = 'Approved for Procurement'
 WHERE chain_key = 'requisition';

-- The two material paths already ended on a desk that names the real outcome.
-- Setting them explicitly stops the terminal depending on row order.
UPDATE requisition_approval_desks SET terminal_label = 'Issued'
 WHERE chain_key = 'material.stores';

UPDATE requisition_approval_desks SET terminal_label = 'Follow-up Complete'
 WHERE chain_key = 'material.procurement';

-- Reject and amend have always had to say why. Cancel did not, and that was the
-- way round it: an administrator could end somebody else's request and the
-- record would show the override without the reason for it. Withdrawing your own
-- request stays free-form — it is yours — so the constraint binds only the
-- override, which is the case an audit asks about.
--
-- The engine enforces this too. The constraint means no script, backfill or
-- future handler can write a silent refusal into the trail.
ALTER TABLE requisition_approval_steps
    DROP CONSTRAINT IF EXISTS requisition_approval_steps_reason_required;

ALTER TABLE requisition_approval_steps
    ADD CONSTRAINT requisition_approval_steps_reason_required
    CHECK (
        length(btrim(reason)) > 0
        OR (action NOT IN ('reject', 'amend') AND NOT (action = 'cancel' AND override))
    );
