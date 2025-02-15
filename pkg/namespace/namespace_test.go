package namespace

import (
	"context"
	"fmt"
	"testing"

	"github.com/EdgeNet-project/edgenet/pkg/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	testclient "k8s.io/client-go/kubernetes/fake"
)

func TestOperations(t *testing.T) {
	Clientset = testclient.NewSimpleClientset()

	list := []string{"test-1", "test-2", "test1", "test2", "test3", "test4", "test5"}
	for _, name := range list {
		namespaceObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		_, err := Clientset.CoreV1().Namespaces().Create(context.TODO(), namespaceObj, metav1.CreateOptions{})
		util.OK(t, err)
	}

	cases := []struct {
		namespace string
		expected  bool
	}{
		{"test-1", true},
		{"test-2", true},
		{"test1", true},
		{"test2", true},
		{"test3", true},
		{"test4", true},
		{"test5", true},
		{"test-3", false},
		{"test-4", false},
		{"test-5", false},
		{"tes-2", false},
		{"test51", false},
	}
	for k, tc := range cases {
		t.Run(fmt.Sprintf("get case %d", k), func(t *testing.T) {
			_, err := GetNamespace(tc.namespace)
			util.Equals(t, tc.expected, !errors.IsNotFound(err))
		})
	}

	t.Run("list", func(t *testing.T) {
		namespaceList := List()
		util.Equals(t, list, namespaceList)
	})
}

func TestSetAsOwnerReference(t *testing.T) {
	cases := []struct {
		name     string
		expected []metav1.OwnerReference
	}{
		{
			"test-1",
			[]metav1.OwnerReference{
				{
					Kind:       "Namespace",
					Name:       "test-1",
					APIVersion: "v1",
				},
			},
		},
		{
			"test1",
			[]metav1.OwnerReference{
				{
					Kind:       "Namespace",
					Name:       "test1",
					APIVersion: "v1",
				},
			},
		},
		{
			"test-2",
			[]metav1.OwnerReference{
				{
					Kind:       "Namespace",
					Name:       "test-2",
					APIVersion: "v1",
				},
			},
		},
	}
	for _, tc := range cases {
		namespaceObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: tc.name}}
		result := SetAsOwnerReference(namespaceObj)
		tc.expected[0].Controller = result[0].Controller
		tc.expected[0].BlockOwnerDeletion = result[0].BlockOwnerDeletion

		util.Equals(t, tc.expected, result)
	}
}
