-- 033: Finance plan mirror — stable code for envelopes synced from
-- finance.budget.upserted (FIN:{entity}:{period}:{account}:{cc|_}).
CREATE UNIQUE INDEX IF NOT EXISTS budgets_finance_plan_code_uidx
    ON budgets (code)
    WHERE code LIKE 'FIN:%';
