package controller

import (
	"context"
	"fmt"
	"strings"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	"github.com/sealos-apps/devbox/v2/controller/internal/controller/helper"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	managedKubeAccessRoleKind = "Role"
)

func isKubeAccessEnabled(devbox *devboxv1alpha2.Devbox) bool {
	return devbox != nil && devbox.Spec.KubeAccess != nil && devbox.Spec.KubeAccess.Enabled
}

func resolveKubeAccessRoleTemplate(
	spec *devboxv1alpha2.KubeAccessSpec,
) (devboxv1alpha2.KubeAccessRoleTemplate, error) {
	if spec == nil || !spec.Enabled || strings.TrimSpace(string(spec.RoleTemplate)) == "" {
		return devboxv1alpha2.KubeAccessRoleTemplateEdit, nil
	}

	switch spec.RoleTemplate {
	case devboxv1alpha2.KubeAccessRoleTemplateView,
		devboxv1alpha2.KubeAccessRoleTemplateEdit,
		devboxv1alpha2.KubeAccessRoleTemplateAdmin:
		return spec.RoleTemplate, nil
	default:
		return "", fmt.Errorf("unsupported kube access role template %q", spec.RoleTemplate)
	}
}

func managedKubeAccessName(devboxName string) string {
	return devboxName + "-" + helper.ManagedKubeAccessNameSuffix
}

func buildManagedServiceAccount(
	devbox *devboxv1alpha2.Devbox,
	recLabels map[string]string,
) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      managedKubeAccessName(devbox.Name),
			Namespace: devbox.Namespace,
			Labels:    recLabels,
		},
	}
}

func buildManagedRole(
	devbox *devboxv1alpha2.Devbox,
	recLabels map[string]string,
	roleTemplate devboxv1alpha2.KubeAccessRoleTemplate,
) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      managedKubeAccessName(devbox.Name),
			Namespace: devbox.Namespace,
			Labels:    recLabels,
		},
		Rules: managedKubeAccessPolicyRules(roleTemplate),
	}
}

func buildManagedRoleBinding(
	devbox *devboxv1alpha2.Devbox,
	recLabels map[string]string,
	roleTemplate devboxv1alpha2.KubeAccessRoleTemplate,
) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      managedKubeAccessName(devbox.Name),
			Namespace: devbox.Namespace,
			Labels:    recLabels,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      managedKubeAccessName(devbox.Name),
				Namespace: devbox.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     managedKubeAccessRoleKind,
			Name:     managedKubeAccessName(devbox.Name),
		},
	}
}

func managedKubeAccessPolicyRules(
	roleTemplate devboxv1alpha2.KubeAccessRoleTemplate,
) []rbacv1.PolicyRule {
	switch roleTemplate {
	case devboxv1alpha2.KubeAccessRoleTemplateView:
		return []rbacv1.PolicyRule{
			{
				APIGroups: []string{"", "apps", "batch", "extensions", "networking.k8s.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
		}
	case devboxv1alpha2.KubeAccessRoleTemplateAdmin:
		return []rbacv1.PolicyRule{
			{
				APIGroups: []string{"*"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
		}
	case devboxv1alpha2.KubeAccessRoleTemplateEdit:
		return []rbacv1.PolicyRule{
			{
				APIGroups: []string{"*"},
				Resources: []string{"*"},
				Verbs: []string{
					"get",
					"list",
					"watch",
					"create",
					"update",
					"patch",
					"delete",
				},
			},
		}
	default:
		return []rbacv1.PolicyRule{}
	}
}

func (r *DevboxReconciler) syncKubeAccess(
	ctx context.Context,
	devbox *devboxv1alpha2.Devbox,
	recLabels map[string]string,
) error {
	roleTemplate, err := resolveKubeAccessRoleTemplate(devbox.Spec.KubeAccess)
	if err != nil {
		return err
	}

	serviceAccount := buildManagedServiceAccount(devbox, recLabels)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, serviceAccount, func() error {
		serviceAccount.Labels = recLabels
		return controllerutil.SetControllerReference(devbox, serviceAccount, r.Scheme)
	}); err != nil {
		return fmt.Errorf("failed to sync kube access serviceaccount: %w", err)
	}

	role := buildManagedRole(devbox, recLabels, roleTemplate)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Labels = recLabels
		role.Rules = managedKubeAccessPolicyRules(roleTemplate)
		return controllerutil.SetControllerReference(devbox, role, r.Scheme)
	}); err != nil {
		return fmt.Errorf("failed to sync kube access role: %w", err)
	}

	roleBinding := buildManagedRoleBinding(devbox, recLabels, roleTemplate)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, roleBinding, func() error {
		roleBinding.Labels = recLabels
		roleBinding.Subjects = []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      managedKubeAccessName(devbox.Name),
				Namespace: devbox.Namespace,
			},
		}
		roleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     managedKubeAccessRoleKind,
			Name:     managedKubeAccessName(devbox.Name),
		}
		return controllerutil.SetControllerReference(devbox, roleBinding, r.Scheme)
	}); err != nil {
		return fmt.Errorf("failed to sync kube access rolebinding: %w", err)
	}

	return nil
}

func (r *DevboxReconciler) deleteManagedKubeAccess(
	ctx context.Context,
	devbox *devboxv1alpha2.Devbox,
) error {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      managedKubeAccessName(devbox.Name),
			Namespace: devbox.Namespace,
		},
	}
	if err := r.Delete(ctx, serviceAccount); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete kube access serviceaccount: %w", err)
	}

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      managedKubeAccessName(devbox.Name),
			Namespace: devbox.Namespace,
		},
	}
	if err := r.Delete(ctx, role); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete kube access role: %w", err)
	}

	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      managedKubeAccessName(devbox.Name),
			Namespace: devbox.Namespace,
		},
	}
	if err := r.Delete(ctx, roleBinding); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete kube access rolebinding: %w", err)
	}
	return nil
}
