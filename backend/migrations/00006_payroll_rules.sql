-- +goose Up
-- Task 3: payroll rule sets with validity periods, and per-employee monthly
-- payroll results. Amounts are integer kuruş; rates are integers in units of
-- 1/100000 (14% = 14000, 0.759% = 759) so no floating point is involved.

CREATE TABLE payroll_rule_sets (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    version              text   NOT NULL UNIQUE,
    valid_during         daterange NOT NULL CHECK (NOT isempty(valid_during) AND lower_inc(valid_during)),
    min_wage_gross       bigint NOT NULL CHECK (min_wage_gross > 0),
    sgk_floor            bigint NOT NULL CHECK (sgk_floor > 0),
    sgk_ceiling          bigint NOT NULL CHECK (sgk_ceiling > sgk_floor),
    sgk_employee_rate    integer NOT NULL CHECK (sgk_employee_rate BETWEEN 0 AND 100000),
    unemp_employee_rate  integer NOT NULL CHECK (unemp_employee_rate BETWEEN 0 AND 100000),
    sgk_employer_rate    integer NOT NULL CHECK (sgk_employer_rate BETWEEN 0 AND 100000),
    employer_discount    integer NOT NULL CHECK (employer_discount BETWEEN 0 AND sgk_employer_rate),
    unemp_employer_rate  integer NOT NULL CHECK (unemp_employer_rate BETWEEN 0 AND 100000),
    stamp_tax_rate       integer NOT NULL CHECK (stamp_tax_rate BETWEEN 0 AND 100000),
    -- Annual wage-income tariff: [{"upto": kuruş or null, "rate": 15000}, ...] ascending.
    tax_brackets         jsonb  NOT NULL CHECK (jsonb_typeof(tax_brackets) = 'array' AND jsonb_array_length(tax_brackets) > 0),
    source               text   NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    EXCLUDE USING gist (valid_during WITH &&)
);

-- Sources:
-- 2025: minimum wage 26.005,50 TL; SGK ceiling 7.5 x minimum wage; employer
--       MYÖ 11% (SGK employer 20.75%); 5510 art. 81/(ı) discount 4 points
--       outside manufacturing; GVGT 329 wage tariff 158.000 / 330.000 /
--       1.200.000 / 4.300.000.
-- 2026: minimum wage 33.030 TL (RG 26.12.2025); Law 7566: SGK ceiling 9 x
--       minimum wage, employer MYÖ 12% (SGK employer 21.75%), discount 2
--       points outside manufacturing; GVGT 332 wage tariff 190.000 /
--       400.000 / 1.500.000 / 5.300.000.
-- Stamp tax 0.759% (since 2015). The company is not in manufacturing.
INSERT INTO payroll_rule_sets (version, valid_during, min_wage_gross, sgk_floor, sgk_ceiling,
        sgk_employee_rate, unemp_employee_rate, sgk_employer_rate, employer_discount, unemp_employer_rate,
        stamp_tax_rate, tax_brackets, source) VALUES
    ('TR-2025.1', '[2025-01-01,2026-01-01)', 2600550, 2600550, 19504125,
        14000, 1000, 20750, 4000, 2000, 759,
        '[{"upto": 15800000, "rate": 15000}, {"upto": 33000000, "rate": 20000},
          {"upto": 120000000, "rate": 27000}, {"upto": 430000000, "rate": 35000}, {"upto": null, "rate": 40000}]',
        'Asgari ücret 2025; 5510 s.K. md. 81/(ı); GVGT Seri No 329'),
    ('TR-2026.1', '[2026-01-01,2027-01-01)', 3303000, 3303000, 29727000,
        14000, 1000, 21750, 2000, 2000, 759,
        '[{"upto": 19000000, "rate": 15000}, {"upto": 40000000, "rate": 20000},
          {"upto": 150000000, "rate": 27000}, {"upto": 530000000, "rate": 35000}, {"upto": null, "rate": 40000}]',
        'Asgari ücret 2026 (RG 26.12.2025); 7566 s.K.; GVGT Seri No 332');

-- One result per employee and month. inputs and result are the full,
-- explainable calculation; input_hash detects whether a recalculation with
-- the same rule set version would change anything.
CREATE TABLE payroll_results (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    employee_id     bigint NOT NULL REFERENCES employees (id),
    period          date   NOT NULL CHECK (extract(day FROM period) = 1),
    rule_version    text   NOT NULL REFERENCES payroll_rule_sets (version),
    input_hash      bytea  NOT NULL,
    inputs          jsonb  NOT NULL,
    result          jsonb  NOT NULL,
    gross           bigint NOT NULL,
    tax_base        bigint NOT NULL,
    cum_tax_base    bigint NOT NULL,
    income_tax      bigint NOT NULL,
    net             bigint NOT NULL,
    employer_cost   bigint NOT NULL,
    calculated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (employee_id, period),
    CHECK (cum_tax_base >= tax_base)
);
CREATE INDEX payroll_results_period_idx ON payroll_results (period);

-- +goose Down
DROP TABLE payroll_results;
DROP TABLE payroll_rule_sets;
