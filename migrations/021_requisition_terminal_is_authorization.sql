-- The last money desk on both chains was labelled "Make payment" and left the
-- request reading "Paid". Neither chain can pay anything. Approving a desk
-- moves the approval state, encumbers the budget and emits an event; no payment
-- record is written, no AP item is raised, and no cash moves.
--
-- So the audit trail asserted a disbursement that never happened. A requisition
-- could reach "Paid" with no purchase order, no goods receipt, no invoice and no
-- three-way match — while the real payment path enforces exactly those controls
-- (ApproveInvoice refuses to clear an invoice until its match resolves).
--
-- What these desks actually do is authorize payment. That is a real and separate
-- control, and it is the term the platform already uses: contract-management
-- clears milestone payments through Payment Authorization and finance consumes
-- contracts.payment.authorized. The labels now say that.
--
-- Encumbrance accounting keeps commitment, expenditure and disbursement as three
-- distinct events with three different authorizing officers. These chains
-- authorize the first and the third; they perform neither.

UPDATE requisition_approval_desks
   SET action_label = 'Authorize payment',
       status_label = 'Payment Authorized'
 WHERE (chain_key, desk) IN (('requisition', 'finance'), ('material.procurement', 'finance_pay'))
   AND status_label = 'Paid';

-- Deliberate divergence from financialtooliag, recorded so the next person
-- comparing the two does not read it as drift.
--
-- That codebase removed the Project Manager desk from its payment chains, and
-- was right to: its payment-requests chain authorizes a disbursement, and a
-- project manager has no standing to authorize cash leaving the bank.
--
-- Our 'requisition' chain authorizes a commitment — approving it encumbers the
-- budget. The authorizing officer for a commitment is the budget holder, which
-- for project spend is the project manager. Dropping that desk would remove the
-- only budget-holder control on the chain and hand first approval to an accounts
-- assistant, who owns neither the budget nor the operational judgement of
-- whether the spend is needed. Our desk is also scoped to project_owner, so it
-- is the request's own manager who must approve, not any holder of the role.
--
-- Same principle, opposite conclusion, because it is a different object. The PM
-- desk stays.
