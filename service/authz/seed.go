package authz

import (
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func seedBuiltInAuthorization(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := seedBuiltInRoles(tx); err != nil {
			return err
		}
		subjects := make([]string, 0, len(builtInRoles))
		rules := make([]model.CasbinRule, 0)
		for _, spec := range builtInRoles {
			subject := RoleSubject(spec.Key)
			subjects = append(subjects, subject)
			if spec.Superuser {
				continue
			}
			for _, permission := range PermissionsForRole(spec.Key) {
				rules = append(rules, newRule("p", []string{
					subject,
					permission.Resource,
					permission.Action,
					EffectAllow,
				}))
			}
		}
		if err := tx.Where("ptype = ? AND v0 IN ?", "p", subjects).Delete(&model.CasbinRule{}).Error; err != nil {
			return err
		}
		if len(rules) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rules).Error
	})
}

func seedBuiltInRoles(db *gorm.DB) error {
	for _, spec := range builtInRoles {
		role := model.AuthzRole{
			Key:         spec.Key,
			Name:        spec.Name,
			Description: spec.Description,
			BuiltIn:     spec.BuiltIn,
			Enabled:     true,
			Sort:        spec.Sort,
		}
		if err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"name",
				"description",
				"built_in",
				"enabled",
				"sort",
			}),
		}).Create(&role).Error; err != nil {
			return err
		}
	}
	return nil
}
