package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A syntactically valid UUID that gen_random_uuid() will never produce
// collisions with in a freshly truncated table.
const (
	missingInventoryLocationID = "00000000-0000-0000-0000-0000000000ff"
	newInventoryLocationID     = "33333333-3333-3333-3333-333333333333"
)

// newInventoryLocationsTestPool connects to the database pointed at by
// DATABASE_URL and makes sure the inventory_locations table exists and is
// empty. Tests that need a database are skipped when DATABASE_URL is not set.
func newInventoryLocationsTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	ctx, pool := newTestPool(t)

	stmt := `CREATE TABLE IF NOT EXISTS butiks_engine.inventory_locations (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name TEXT NOT NULL,
		code TEXT NOT NULL UNIQUE,
		address TEXT,
		is_active BOOLEAN DEFAULT TRUE,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`
	if _, err := pool.Exec(ctx, stmt); err != nil {
		t.Fatalf("preparing inventory_locations schema: %v", err)
	}

	truncate := func() {
		if _, err := pool.Exec(ctx, `TRUNCATE butiks_engine.inventory_locations RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncating inventory_locations: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	return ctx, pool
}

// seedInventoryLocation inserts a location with an explicit created_at so
// tests can assert on ordering, and returns its generated id.
func seedInventoryLocation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, code, address string, isActive bool, createdAt time.Time) string {
	t.Helper()

	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO butiks_engine.inventory_locations (name, code, address, is_active, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $5)
		 RETURNING id`,
		name, code, address, isActive, createdAt,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seeding inventory location %q: %v", code, err)
	}
	return id
}

func TestInventoryLocationsRepository(t *testing.T) {
	t.Run("resolveInventoryLocationsPagination", func(t *testing.T) {
		ptr := func(i int) *int { return &i }

		cases := []struct {
			name       string
			params     *GetInventoryLocationsParams
			wantLimit  int
			wantOffset int
		}{
			{"nil params -> defaults", nil, defaultInventoryLocationsLimit, 0},
			{"empty params -> defaults", &GetInventoryLocationsParams{}, defaultInventoryLocationsLimit, 0},
			{"explicit limit and offset", &GetInventoryLocationsParams{Limit: ptr(10), Offset: ptr(20)}, 10, 20},
			{"limit above max is capped", &GetInventoryLocationsParams{Limit: ptr(maxInventoryLocationsLimit + 1)}, maxInventoryLocationsLimit, 0},
			{"zero limit -> default", &GetInventoryLocationsParams{Limit: ptr(0)}, defaultInventoryLocationsLimit, 0},
			{"negative limit -> default", &GetInventoryLocationsParams{Limit: ptr(-5)}, defaultInventoryLocationsLimit, 0},
			{"negative offset -> zero", &GetInventoryLocationsParams{Offset: ptr(-5)}, defaultInventoryLocationsLimit, 0},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				gotLimit, gotOffset := resolveInventoryLocationsPagination(tc.params)
				if gotLimit != tc.wantLimit || gotOffset != tc.wantOffset {
					t.Fatalf("resolveInventoryLocationsPagination(%+v) = (%d, %d), want (%d, %d)",
						tc.params, gotLimit, gotOffset, tc.wantLimit, tc.wantOffset)
				}
			})
		}
	})

	t.Run("CountInventoryLocations", func(t *testing.T) {
		ctx, pool := newInventoryLocationsTestPool(t)
		repo := NewInventoryLocationsRepositoryHandler(ctx, pool, nil)

		count, err := repo.CountInventoryLocations()
		if err != nil {
			t.Fatalf("CountInventoryLocations on empty table: %v", err)
		}
		if count != 0 {
			t.Fatalf("CountInventoryLocations on empty table = %d, want 0", count)
		}

		now := time.Now().UTC()
		for i := range 3 {
			seedInventoryLocation(t, ctx, pool,
				fmt.Sprintf("Warehouse %d", i),
				fmt.Sprintf("wh-%d", i),
				"an address",
				true,
				now.Add(time.Duration(i)*time.Minute),
			)
		}

		count, err = repo.CountInventoryLocations()
		if err != nil {
			t.Fatalf("CountInventoryLocations after seeding: %v", err)
		}
		if count != 3 {
			t.Fatalf("CountInventoryLocations after seeding = %d, want 3", count)
		}
	})

	t.Run("GetInventoryLocations empty table returns empty slice", func(t *testing.T) {
		ctx, pool := newInventoryLocationsTestPool(t)
		repo := NewInventoryLocationsRepositoryHandler(ctx, pool, nil)

		locations, err := repo.GetInventoryLocations(nil)
		if err != nil {
			t.Fatalf("GetInventoryLocations on empty table: %v", err)
		}
		if locations == nil {
			t.Fatal("GetInventoryLocations returned nil slice, want non-nil empty slice")
		}
		if len(locations) != 0 {
			t.Fatalf("GetInventoryLocations on empty table returned %d locations, want 0", len(locations))
		}
	})

	t.Run("GetInventoryLocations maps columns and orders by created_at desc", func(t *testing.T) {
		ctx, pool := newInventoryLocationsTestPool(t)
		repo := NewInventoryLocationsRepositoryHandler(ctx, pool, nil)

		base := time.Now().UTC().Truncate(time.Second)
		oldID := seedInventoryLocation(t, ctx, pool, "Old", "old", "old address", true, base.Add(-2*time.Hour))
		midID := seedInventoryLocation(t, ctx, pool, "Mid", "mid", "mid address", true, base.Add(-1*time.Hour))
		newID := seedInventoryLocation(t, ctx, pool, "New", "new", "new address", false, base)

		locations, err := repo.GetInventoryLocations(nil)
		if err != nil {
			t.Fatalf("GetInventoryLocations: %v", err)
		}
		if len(locations) != 3 {
			t.Fatalf("GetInventoryLocations returned %d locations, want 3", len(locations))
		}

		wantOrder := []string{newID, midID, oldID}
		for i, want := range wantOrder {
			if locations[i].ID != want {
				t.Fatalf("locations[%d].ID = %s, want %s (order should be created_at desc)", i, locations[i].ID, want)
			}
		}

		first := locations[0]
		if first.Name != "New" || first.Code != "new" || first.Address != "new address" || first.IsActive {
			t.Fatalf("first location mapped incorrectly: %+v", first)
		}
		if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
			t.Fatalf("timestamps not populated: %+v", first)
		}
	})

	t.Run("GetInventoryLocations respects limit and offset", func(t *testing.T) {
		ctx, pool := newInventoryLocationsTestPool(t)
		repo := NewInventoryLocationsRepositoryHandler(ctx, pool, nil)

		base := time.Now().UTC().Truncate(time.Second)
		ids := make([]string, 5)
		for i := range 5 {
			ids[i] = seedInventoryLocation(t, ctx, pool,
				fmt.Sprintf("L%d", i),
				fmt.Sprintf("l-%d", i),
				"addr",
				true,
				base.Add(time.Duration(-i)*time.Minute),
			)
		}
		// ids[0] is newest, ids[4] is oldest -> result order is ids[0..4].

		limit := 2
		offset := 1
		locations, err := repo.GetInventoryLocations(&GetInventoryLocationsParams{Limit: &limit, Offset: &offset})
		if err != nil {
			t.Fatalf("GetInventoryLocations with limit/offset: %v", err)
		}
		if len(locations) != 2 {
			t.Fatalf("GetInventoryLocations returned %d locations, want 2", len(locations))
		}
		if locations[0].ID != ids[1] || locations[1].ID != ids[2] {
			t.Fatalf("GetInventoryLocations limit=2 offset=1 = [%s %s], want [%s %s]",
				locations[0].ID, locations[1].ID, ids[1], ids[2])
		}
	})

	t.Run("GetInventoryLocationByID maps columns for an existing location", func(t *testing.T) {
		ctx, pool := newInventoryLocationsTestPool(t)
		repo := NewInventoryLocationsRepositoryHandler(ctx, pool, nil)

		base := time.Now().UTC().Truncate(time.Second)
		id := seedInventoryLocation(t, ctx, pool, "Main Warehouse", "main-wh", "123 Main St", true, base)

		got, err := repo.GetInventoryLocationByID(id)
		if err != nil {
			t.Fatalf("GetInventoryLocationByID: %v", err)
		}
		if got.ID != id || got.Name != "Main Warehouse" || got.Code != "main-wh" || got.Address != "123 Main St" || !got.IsActive {
			t.Fatalf("GetInventoryLocationByID mapped incorrectly: %+v", got)
		}
		if !got.CreatedAt.Equal(base) || !got.UpdatedAt.Equal(base) {
			t.Fatalf("GetInventoryLocationByID timestamps = (%s, %s), want %s", got.CreatedAt, got.UpdatedAt, base)
		}
	})

	t.Run("GetInventoryLocationByID returns pgx.ErrNoRows when the id is unknown", func(t *testing.T) {
		ctx, pool := newInventoryLocationsTestPool(t)
		repo := NewInventoryLocationsRepositoryHandler(ctx, pool, nil)

		seedInventoryLocation(t, ctx, pool, "Other", "other", "unrelated", true, time.Now().UTC())

		got, err := repo.GetInventoryLocationByID(missingInventoryLocationID)
		if err == nil {
			t.Fatalf("GetInventoryLocationByID(unknown) = %+v, want an error", got)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("GetInventoryLocationByID(unknown) error = %v, want pgx.ErrNoRows", err)
		}
		if got != nil {
			t.Fatalf("GetInventoryLocationByID(unknown) returned non-nil location %+v", got)
		}
	})

	t.Run("DeleteInventoryLocationByID removes only the target row", func(t *testing.T) {
		ctx, pool := newInventoryLocationsTestPool(t)
		repo := NewInventoryLocationsRepositoryHandler(ctx, pool, nil)

		now := time.Now().UTC()
		keepID := seedInventoryLocation(t, ctx, pool, "Keep", "keep", "stays", true, now)
		dropID := seedInventoryLocation(t, ctx, pool, "Drop", "drop", "goes", true, now)

		if err := repo.DeleteInventoryLocationByID(dropID); err != nil {
			t.Fatalf("DeleteInventoryLocationByID: %v", err)
		}

		if _, err := repo.GetInventoryLocationByID(dropID); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("after delete, GetInventoryLocationByID(dropID) error = %v, want pgx.ErrNoRows", err)
		}
		if _, err := repo.GetInventoryLocationByID(keepID); err != nil {
			t.Fatalf("DeleteInventoryLocationByID removed the wrong row: %v", err)
		}

		count, err := repo.CountInventoryLocations()
		if err != nil {
			t.Fatalf("CountInventoryLocations: %v", err)
		}
		if count != 1 {
			t.Fatalf("CountInventoryLocations after delete = %d, want 1", count)
		}
	})

	t.Run("DeleteInventoryLocationByID is a no-op for an unknown id", func(t *testing.T) {
		ctx, pool := newInventoryLocationsTestPool(t)
		repo := NewInventoryLocationsRepositoryHandler(ctx, pool, nil)

		seedInventoryLocation(t, ctx, pool, "Keep", "keep", "stays", true, time.Now().UTC())

		if err := repo.DeleteInventoryLocationByID(missingInventoryLocationID); err != nil {
			t.Fatalf("DeleteInventoryLocationByID(unknown) = %v, want nil", err)
		}

		count, err := repo.CountInventoryLocations()
		if err != nil {
			t.Fatalf("CountInventoryLocations: %v", err)
		}
		if count != 1 {
			t.Fatalf("CountInventoryLocations after no-op delete = %d, want 1", count)
		}
	})

	t.Run("SaveInventoryLocation inserts when the id is new", func(t *testing.T) {
		ctx, pool := newInventoryLocationsTestPool(t)
		repo := NewInventoryLocationsRepositoryHandler(ctx, pool, nil)

		created := time.Now().UTC().Truncate(time.Second)
		l := &butik_domain.InventoryLocation{
			ID:        newInventoryLocationID,
			Name:      "Depot",
			Code:      "depot",
			Address:   "456 Depot Ave",
			IsActive:  true,
			CreatedAt: created,
			UpdatedAt: created,
		}

		if err := repo.SaveInventoryLocation(l); err != nil {
			t.Fatalf("SaveInventoryLocation insert: %v", err)
		}

		got, err := repo.GetInventoryLocationByID(newInventoryLocationID)
		if err != nil {
			t.Fatalf("GetInventoryLocationByID after insert: %v", err)
		}
		if got.Name != "Depot" || got.Code != "depot" || got.Address != "456 Depot Ave" || !got.IsActive {
			t.Fatalf("SaveInventoryLocation stored the wrong values: %+v", got)
		}
		if !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(created) {
			t.Fatalf("SaveInventoryLocation timestamps = (%s, %s), want %s", got.CreatedAt, got.UpdatedAt, created)
		}
	})

	t.Run("SaveInventoryLocation updates in place when the id already exists", func(t *testing.T) {
		ctx, pool := newInventoryLocationsTestPool(t)
		repo := NewInventoryLocationsRepositoryHandler(ctx, pool, nil)

		created := time.Now().UTC().Truncate(time.Second)
		id := seedInventoryLocation(t, ctx, pool, "Old name", "old-code", "old address", true, created)

		updated := created.Add(time.Hour)
		l := &butik_domain.InventoryLocation{
			ID:        id,
			Name:      "New name",
			Code:      "new-code",
			Address:   "new address",
			IsActive:  false,
			CreatedAt: created,
			UpdatedAt: updated,
		}

		if err := repo.SaveInventoryLocation(l); err != nil {
			t.Fatalf("SaveInventoryLocation update: %v", err)
		}

		got, err := repo.GetInventoryLocationByID(id)
		if err != nil {
			t.Fatalf("GetInventoryLocationByID after update: %v", err)
		}
		if got.Name != "New name" || got.Code != "new-code" || got.Address != "new address" || got.IsActive {
			t.Fatalf("SaveInventoryLocation did not update the values: %+v", got)
		}
		if !got.UpdatedAt.Equal(updated) {
			t.Fatalf("SaveInventoryLocation updated_at = %s, want %s", got.UpdatedAt, updated)
		}

		count, err := repo.CountInventoryLocations()
		if err != nil {
			t.Fatalf("CountInventoryLocations: %v", err)
		}
		if count != 1 {
			t.Fatalf("SaveInventoryLocation upsert created a second row: count = %d, want 1", count)
		}
	})
}
