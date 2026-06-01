-- notification_email_jobs (created in 003_notifications.sql) was never
-- consumed by any Go code — procurement has no outbox under the central
-- iag-notifications model. Drop the dead table.
--
-- Pre-schema-isolation deploys created it in public; post-isolation
-- (commit 597d0b4) the same migration creates it in the procurement
-- schema. Drop from both so this cleans up Railway deployments that
-- straddle the isolation cutover.
DROP TABLE IF EXISTS procurement.notification_email_jobs;
DROP TABLE IF EXISTS public.notification_email_jobs;
