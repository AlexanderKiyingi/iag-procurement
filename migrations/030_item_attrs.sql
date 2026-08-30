-- 030: an extension bag on the purchasing catalogue.
--
-- `items` is the register of everything bought or sold that carries no stock
-- position — a service, a fee, a consumable nobody counts. Clients that present
-- it as a catalogue hold more about an item than procurement needs: which GL
-- account a sale posts to, what it sells for, whether the line is still offered.
-- None of that is procurement's business and all of it was being dropped,
-- because there was nowhere for it to go.
--
-- A bag rather than typed columns, deliberately. A sales account is finance's
-- concept and a status is the presenting app's; typing them here would make
-- this service look like it has an opinion about them, and the next client
-- would reasonably expect it to enforce one. iag-warehouse already carries the
-- same app's commercial view of an item this way, so the pattern is the
-- platform's, not an invention.

ALTER TABLE items
    ADD COLUMN IF NOT EXISTS attrs JSONB NOT NULL DEFAULT '{}';

COMMENT ON COLUMN items.attrs IS
    'Caller-owned extension bag. Procurement stores and returns it and never reads it.';
