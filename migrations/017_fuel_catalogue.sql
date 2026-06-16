-- Fuel procurement catalogue. Fuel buying reuses the existing procure-to-pay
-- engine: fuel suppliers are ordinary vendors carrying category='fuel' (no
-- schema change — vendors.category is free-form, created via POST /vendors), and
-- these canonical fuel line items give fuel requisitions / POs a stable SKU to
-- reference (qty in litres). Idempotent so re-running is a no-op.
-- Statements are separated by blank lines because the migrator splits on ";\n\n".

INSERT INTO items (id, sku, name, category, uom, currency)
VALUES
    ('ITEM-FUEL-DIESEL',   'FUEL-DIESEL',   'Diesel (AGO)',   'fuel', 'litre', 'UGX'),
    ('ITEM-FUEL-PETROL',   'FUEL-PETROL',   'Petrol (PMS)',   'fuel', 'litre', 'UGX'),
    ('ITEM-FUEL-KEROSENE', 'FUEL-KEROSENE', 'Kerosene (BIK)', 'fuel', 'litre', 'UGX')
ON CONFLICT (id) DO NOTHING;
