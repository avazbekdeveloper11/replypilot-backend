package postgres

import (
	"context"

	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/repository"
)

// AdminRepository queries across every tenant — see
// internal/domain/repository.AdminRepository's doc comment for the
// authorization boundary this depends on (RequirePlatformAdmin
// middleware), and platform_admin.go's withPlatformAdmin for the
// mechanism.
type AdminRepository struct {
	db *gorm.DB
}

func NewAdminRepository(db *gorm.DB) *AdminRepository {
	return &AdminRepository{db: db}
}

// ListOrganizations combines three queries: organizations itself has no
// RLS at all (plain query, see model.go's doc comment on OrganizationModel
// vs. the tenant_isolation loop), while active member counts and current
// subscription/plan need the platform_admin GUC since team_members and
// subscriptions are both RLS-protected. Joined in Go rather than one giant
// SQL join — organizations is expected to be a few hundred rows at most
// for an early-stage product, not a scale where N+1-shaped Go-side joins
// matter; revisit if that stops being true.
func (r *AdminRepository) ListOrganizations(ctx context.Context) ([]repository.OrganizationSummary, error) {
	var orgModels []OrganizationModel
	if err := r.db.WithContext(ctx).Order("created_at DESC").Find(&orgModels).Error; err != nil {
		return nil, apperror.Internal("list organizations", err)
	}

	var memberCounts []struct {
		OrganizationID string
		Count          int64
	}
	var subs []struct {
		OrganizationID string
		Status         string
		PlanCode       string
	}
	err := withPlatformAdmin(ctx, r.db, func(tx *gorm.DB) error {
		if err := tx.Model(&TeamMemberModel{}).
			Select("organization_id, count(*) as count").
			Where("status = ?", "active").
			Group("organization_id").
			Scan(&memberCounts).Error; err != nil {
			return err
		}
		return tx.Table("subscriptions AS s").
			Select("s.organization_id, s.status, p.code as plan_code").
			Joins("JOIN plans p ON p.id = s.plan_id").
			Where("s.status IN ('trialing','active','past_due','paused')").
			Scan(&subs).Error
	})
	if err != nil {
		return nil, apperror.Internal("list organizations (aggregates)", err)
	}

	memberCountByOrg := make(map[string]int64, len(memberCounts))
	for _, m := range memberCounts {
		memberCountByOrg[m.OrganizationID] = m.Count
	}
	subByOrg := make(map[string]struct {
		Status   string
		PlanCode string
	}, len(subs))
	for _, s := range subs {
		subByOrg[s.OrganizationID] = struct {
			Status   string
			PlanCode string
		}{Status: s.Status, PlanCode: s.PlanCode}
	}

	out := make([]repository.OrganizationSummary, 0, len(orgModels))
	for i := range orgModels {
		org := modelToOrganization(&orgModels[i])
		summary := repository.OrganizationSummary{
			Organization: org,
			MemberCount:  memberCountByOrg[org.ID.String()],
		}
		if s, ok := subByOrg[org.ID.String()]; ok {
			status := s.Status
			plan := s.PlanCode
			summary.SubscriptionStatus = &status
			summary.PlanCode = &plan
		}
		out = append(out, summary)
	}
	return out, nil
}

func (r *AdminRepository) Stats(ctx context.Context) (*repository.PlatformStats, error) {
	stats := &repository.PlatformStats{}

	if err := r.db.WithContext(ctx).Model(&OrganizationModel{}).Count(&stats.TotalOrganizations).Error; err != nil {
		return nil, apperror.Internal("count organizations", err)
	}
	if err := r.db.WithContext(ctx).Model(&UserModel{}).Count(&stats.TotalUsers).Error; err != nil {
		return nil, apperror.Internal("count users", err)
	}

	var subRows []struct {
		Status            string
		PlanCode          string
		PlanName          string
		PriceMonthlyCents int
	}
	err := withPlatformAdmin(ctx, r.db, func(tx *gorm.DB) error {
		if err := tx.Model(&ConversationModel{}).Count(&stats.TotalConversations).Error; err != nil {
			return err
		}
		if err := tx.Model(&MessageModel{}).Count(&stats.TotalMessages).Error; err != nil {
			return err
		}
		return tx.Table("subscriptions AS s").
			Select("s.status, p.code as plan_code, p.name as plan_name, p.price_monthly_cents").
			Joins("JOIN plans p ON p.id = s.plan_id").
			Where("s.status IN ('trialing','active','past_due','paused')").
			Scan(&subRows).Error
	})
	if err != nil {
		return nil, apperror.Internal("compute platform stats", err)
	}

	byPlan := make(map[string]*repository.PlanSubscriptionCount)
	var planOrder []string
	for _, row := range subRows {
		stats.ActiveSubscriptions++
		if row.Status == "active" || row.Status == "trialing" {
			stats.MRRCentsApprox += int64(row.PriceMonthlyCents)
		}
		entry, ok := byPlan[row.PlanCode]
		if !ok {
			entry = &repository.PlanSubscriptionCount{PlanCode: row.PlanCode, PlanName: row.PlanName}
			byPlan[row.PlanCode] = entry
			planOrder = append(planOrder, row.PlanCode)
		}
		entry.Count++
	}
	for _, code := range planOrder {
		stats.SubscriptionsByPlan = append(stats.SubscriptionsByPlan, *byPlan[code])
	}

	return stats, nil
}
