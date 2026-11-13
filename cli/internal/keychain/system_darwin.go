//go:build darwin && cgo

package keychain

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

static CFStringRef maiString(const char *s) {
	return CFStringCreateWithCString(NULL, s, kCFStringEncodingUTF8);
}

// maiQuery returns a new query for the generic password of one service and one account.
// The caller releases it.
static CFMutableDictionaryRef maiQuery(const char *service, const char *account) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef s = maiString(service);
	CFStringRef a = maiString(account);
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecAttrService, s);
	CFDictionarySetValue(q, kSecAttrAccount, a);
	CFRelease(s);
	CFRelease(a);
	return q;
}

static OSStatus maiDelete(const char *service, const char *account) {
	CFMutableDictionaryRef q = maiQuery(service, account);
	OSStatus st = SecItemDelete(q);
	CFRelease(q);
	return st;
}

// maiGet copies the secret of the item into out. The caller releases out. When the access
// list of the item does not hold this program, macOS shows a prompt, unless noUI is 1.
static OSStatus maiGet(const char *service, const char *account, int noUI, CFDataRef *out) {
	CFMutableDictionaryRef q = maiQuery(service, account);
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
	if (noUI) {
		CFDictionarySetValue(q, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail);
	}
	CFTypeRef result = NULL;
	OSStatus st = SecItemCopyMatching(q, &result);
	CFRelease(q);
	if (st != errSecSuccess) {
		return st;
	}
	if (result == NULL || CFGetTypeID(result) != CFDataGetTypeID()) {
		if (result != NULL) {
			CFRelease(result);
		}
		return errSecDecode;
	}
	*out = (CFDataRef)result;
	return errSecSuccess;
}

// maiHas finds the item and reads no secret. Thus macOS shows no prompt.
static OSStatus maiHas(const char *service, const char *account) {
	SecKeychainItemRef item = NULL;
	OSStatus st = SecKeychainFindGenericPassword(NULL,
		(UInt32)strlen(service), service, (UInt32)strlen(account), account,
		NULL, NULL, &item);
	if (item != NULL) {
		CFRelease(item);
	}
	return st;
}

// maiDefaultUnlocked returns 1 when the default keychain is unlocked and writable.
static int maiDefaultUnlocked(void) {
	SecKeychainRef kc = NULL;
	if (SecKeychainCopyDefault(&kc) != errSecSuccess || kc == NULL) {
		return 0;
	}
	SecKeychainStatus status = 0;
	OSStatus st = SecKeychainGetStatus(kc, &status);
	CFRelease(kc);
	if (st != errSecSuccess) {
		return 0;
	}
	return (status & kSecUnlockStateStatus) && (status & kSecWritePermStatus) ? 1 : 0;
}

// maiErrorMessage writes the text of a status into buf.
static void maiErrorMessage(OSStatus st, char *buf, long size) {
	buf[0] = 0;
	CFStringRef msg = SecCopyErrorMessageString(st, NULL);
	if (msg == NULL) {
		return;
	}
	CFStringGetCString(msg, buf, (CFIndex)size, kCFStringEncodingUTF8);
	CFRelease(msg);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Status codes of the Security framework.
const (
	errSecItemNotFound      = -25300
	errSecNoSuchKeychain    = -25294
	errSecNoDefaultKeychain = -25307
)

// system returns the keychain of the OS for service. On macOS it is the Security framework.
func system(service string) Store { return securityStore{service: service} }

// securityStore is a Store on the Security framework of macOS for one service. When noUI is
// true, a read that needs a prompt fails and macOS shows no prompt.
type securityStore struct {
	service string
	noUI    bool
}

func (s securityStore) Get(account string) (string, error) {
	cs, ca := C.CString(s.service), C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	noUI := C.int(0)
	if s.noUI {
		noUI = 1
	}
	var out C.CFDataRef
	if st := C.maiGet(cs, ca, noUI, &out); st != C.errSecSuccess {
		return "", statusError("get", st)
	}
	defer C.CFRelease(C.CFTypeRef(out))
	n := C.CFDataGetLength(out)
	if n == 0 {
		return "", nil
	}
	return C.GoStringN((*C.char)(unsafe.Pointer(C.CFDataGetBytePtr(out))), C.int(n)), nil
}

func (s securityStore) Has(account string) (bool, error) {
	cs, ca := C.CString(s.service), C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	st := C.maiHas(cs, ca)
	if st == C.errSecSuccess {
		return true, nil
	}
	err := statusError("find", st)
	if err == ErrNotFound {
		return false, nil
	}
	return false, err
}

func (s securityStore) Delete(account string) error {
	cs, ca := C.CString(s.service), C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	if st := C.maiDelete(cs, ca); st != C.errSecSuccess {
		return statusError("delete", st)
	}
	return nil
}

// defaultUnlocked tells if the default keychain is unlocked and writable.
func defaultUnlocked() bool { return C.maiDefaultUnlocked() == 1 }

// statusErr is an error status of the Security framework. It holds no secret.
type statusErr struct {
	op      string
	status  int
	message string
}

func (e *statusErr) Error() string {
	if e.message == "" {
		return fmt.Sprintf("keychain: %s: status %d", e.op, e.status)
	}
	return fmt.Sprintf("keychain: %s: %s (status %d)", e.op, e.message, e.status)
}

// statusError gives the error of this package for a status: ErrNotFound for an item that
// does not exist, ErrUnavailable when the user has no keychain, and else a statusErr.
func statusError(op string, st C.OSStatus) error {
	switch int(st) {
	case errSecItemNotFound:
		return ErrNotFound
	case errSecNoSuchKeychain, errSecNoDefaultKeychain:
		return fmt.Errorf("%w: status %d", ErrUnavailable, int(st))
	}
	buf := make([]byte, 256)
	C.maiErrorMessage(st, (*C.char)(unsafe.Pointer(&buf[0])), C.long(len(buf)))
	msg := C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
	return &statusErr{op: op, status: int(st), message: msg}
}
