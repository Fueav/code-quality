SELECT id, COALESCE(region_v2, region) AS region FROM accounts WHERE id = $1;
