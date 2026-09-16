-- 032: the form fields the receiving, payables, contracts and requisition
-- screens already collect but the service dropped on save.
--
-- grns: a quality-critical receipt cannot be posted until QC releases it
-- (enforced in CreateGrn/UpdateGrn), and the warehouse and notes the receiver
-- typed were lost with a 200.
-- invoices: a variance against the PO must be explained before approval
-- (enforced in ApproveInvoice); due_date drives the payables ageing.
-- contracts: committed_volume is the volume the contract binds the vendor to.
-- requisitions: free-text notes from the requester.

ALTER TABLE grns
    ADD COLUMN IF NOT EXISTS quality_critical boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS qc_status text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS warehouse text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS notes text NOT NULL DEFAULT '';

ALTER TABLE invoices
    ADD COLUMN IF NOT EXISTS variance_resolution text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS due_date date;

ALTER TABLE contracts
    ADD COLUMN IF NOT EXISTS committed_volume numeric(18,2) NOT NULL DEFAULT 0;

ALTER TABLE requisitions
    ADD COLUMN IF NOT EXISTS notes text NOT NULL DEFAULT '';
