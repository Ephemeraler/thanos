// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

package tenancy

import (
	"context"
	"net/http"
	"path"

	"github.com/pkg/errors"
	"github.com/prometheus-community/prom-label-proxy/injectproxy"
	"github.com/prometheus/prometheus/model/labels"
	"google.golang.org/grpc/metadata"

	"github.com/thanos-io/thanos/pkg/extpromql"
)

type contextKey int

const (
	// DefaultTenantHeader is the default header used to designate the tenant making a request.
	DefaultTenantHeader = "THANOS-TENANT"
	// 默认租户ID. 当请求头中无法获取租户ID时, 使用默认租户ID.
	DefaultTenant = "default-tenant"
	// DefaultTenantLabel is the default label-name with which the tenant is announced in stored metrics.
	DefaultTenantLabel = "tenant_id"
	// This key is used to pass tenant information using Context.
	TenantKey contextKey = 0
	// MetricLabel is the label name used for adding tenant information to exported metrics.
	MetricLabel = "tenant"
)

// Allowed fields in client certificates.
const (
	CertificateFieldOrganization       = "organization"
	CertificateFieldOrganizationalUnit = "organizationalUnit"
	CertificateFieldCommonName         = "commonName"
)

// IsTenantValid 验证 tenant 有效性. 租户名称必须是路径中的"最末一级"部分，不能包含斜杠(/)或类似路径成分.
func IsTenantValid(tenant string) error {
	if tenant != path.Base(tenant) {
		return errors.New("Tenant name not valid")
	}
	return nil
}

// GetTenantFromHTTP 提取 tenant id 从 http request header 或 cert 中. certTenantField 一旦设置, 只会从 cert 中获取租户ID, 若无法获取则返回错误.
func GetTenantFromHTTP(r *http.Request, tenantHeader string, defaultTenantID string, certTenantField string) (string, error) {
	var err error
	// 获取租户ID, 优先使用自定义 header key. 如果没有, 使用默认 header key. 若没有, 使用默认租户ID.
	tenant := r.Header.Get(tenantHeader)
	if tenant == "" {
		tenant = r.Header.Get(DefaultTenantHeader)
		if tenant == "" {
			tenant = defaultTenantID
		}
	}

	// 从 Certificate 中获取租户ID.
	if certTenantField != "" {
		tenant, err = getTenantFromCertificate(r, certTenantField)
		if err != nil {
			return "", err
		}
	}

	// 验证 tenant id 有效性.
	err = IsTenantValid(tenant)
	if err != nil {
		return "", err
	}
	return tenant, nil
}

// roundTripperFunc http.RoundTripper 接口的函数适配器
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (r roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return r(request)
}

// InternalTenancyConversionTripper 该 http.RoundTripper 对租户信息进行统一处理放入请求头中.
func InternalTenancyConversionTripper(customTenantHeader, certTenantField string, next http.RoundTripper) http.RoundTripper {
	return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		tenant, _ := GetTenantFromHTTP(r, customTenantHeader, DefaultTenant, certTenantField)
		r.Header.Set(DefaultTenantHeader, tenant)
		if customTenantHeader != DefaultTenantHeader {
			r.Header.Del(customTenantHeader)
		}
		return next.RoundTrip(r)
	})
}

// getTenantFromCertificate 用于从 leaf certification中O, OU, CN字段获取租户ID, 当无法获取时报错.
func getTenantFromCertificate(r *http.Request, certTenantField string) (string, error) {
	var tenant string

	// 判断对端是否发送证书链.
	if len(r.TLS.PeerCertificates) == 0 {
		return "", errors.New("could not get required certificate field from client cert")
	}

	// 获取 leaf 证书.
	cert := r.TLS.PeerCertificates[0]

	// 从这里从证书中获取租户ID. 如果无法从证书中获取会直接报错.
	switch certTenantField {
	case CertificateFieldOrganization:
		if len(cert.Subject.Organization) == 0 {
			return "", errors.New("could not get organization field from client cert")
		}
		tenant = cert.Subject.Organization[0]

	case CertificateFieldOrganizationalUnit:
		if len(cert.Subject.OrganizationalUnit) == 0 {
			return "", errors.New("could not get organizationalUnit field from client cert")
		}
		tenant = cert.Subject.OrganizationalUnit[0]

	case CertificateFieldCommonName:
		if cert.Subject.CommonName == "" {
			return "", errors.New("could not get commonName field from client cert")
		}
		tenant = cert.Subject.CommonName

	default:
		return "", errors.New("tls client cert field requested is not supported")
	}

	return tenant, nil
}

func GetTenantFromGRPCMetadata(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(md.Get(DefaultTenantHeader)) == 0 {
		return DefaultTenant, false
	}
	return md.Get(DefaultTenantHeader)[0], true
}

func EnforceQueryTenancy(tenantLabel string, tenant string, query string) (string, error) {
	labelMatcher := &labels.Matcher{
		Name:  tenantLabel,
		Type:  labels.MatchEqual,
		Value: tenant,
	}

	e := injectproxy.NewEnforcer(false, labelMatcher)

	expr, err := extpromql.ParseExpr(query)
	if err != nil {
		return "", errors.Wrap(err, "error parsing query string, when enforcing tenenacy")
	}

	if err := e.EnforceNode(expr); err != nil {
		return "", errors.Wrap(err, "error enforcing label")
	}

	return expr.String(), nil
}

func getLabelMatchers(formMatchers []string, tenant string, enforceTenancy bool, tenantLabel string) ([][]*labels.Matcher, error) {
	tenantLabelMatcher := &labels.Matcher{
		Name:  tenantLabel,
		Type:  labels.MatchEqual,
		Value: tenant,
	}

	matcherSets := make([][]*labels.Matcher, 0, len(formMatchers))

	// If tenancy is enforced, but there are no matchers at all, add the tenant matcher
	if len(formMatchers) == 0 && enforceTenancy {
		var matcher []*labels.Matcher
		matcher = append(matcher, tenantLabelMatcher)
		matcherSets = append(matcherSets, matcher)
		return matcherSets, nil
	}

	for _, s := range formMatchers {
		matchers, err := extpromql.ParseMetricSelector(s)
		if err != nil {
			return nil, err
		}

		if enforceTenancy {
			e := injectproxy.NewEnforcer(false, tenantLabelMatcher)
			matchers, err = e.EnforceMatchers(matchers)
			if err != nil {
				return nil, err
			}
		}

		matcherSets = append(matcherSets, matchers)
	}

	return matcherSets, nil
}

// This function will:
// - Get tenant from HTTP header and add it to context.
// - if tenancy is enforced, add a tenant matcher to the promQL expression.
func RewritePromQL(ctx context.Context, r *http.Request, tenantHeader string, defaultTenantID string, certTenantField string, enforceTenancy bool, tenantLabel string, queryStr string) (string, string, context.Context, error) {
	tenant, err := GetTenantFromHTTP(r, tenantHeader, defaultTenantID, certTenantField)
	if err != nil {
		return "", "", ctx, err
	}
	ctx = context.WithValue(ctx, TenantKey, tenant)

	if enforceTenancy {
		queryStr, err = EnforceQueryTenancy(tenantLabel, tenant, queryStr)
		return queryStr, tenant, ctx, err
	}
	return queryStr, tenant, ctx, nil
}

// This function will:
// - Get tenant from HTTP header and add it to context.
// - Parse all labels matchers provided.
// - If tenancy is enforced, make sure a tenant matcher is present.
func RewriteLabelMatchers(ctx context.Context, r *http.Request, tenantHeader string, defaultTenantID string, certTenantField string, enforceTenancy bool, tenantLabel string, formMatchers []string) ([][]*labels.Matcher, context.Context, error) {
	tenant, err := GetTenantFromHTTP(r, tenantHeader, defaultTenantID, certTenantField)
	if err != nil {
		return nil, ctx, err
	}
	ctx = context.WithValue(ctx, TenantKey, tenant)

	matcherSets, err := getLabelMatchers(formMatchers, tenant, enforceTenancy, tenantLabel)
	if err != nil {
		return nil, ctx, err
	}

	return matcherSets, ctx, nil
}
