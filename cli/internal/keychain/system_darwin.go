//go:build darwin && cgo

package keychain

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
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

// maiSet removes the old item and adds a new item. The access list of the new item holds
// only the executable of this process. Other programs get a prompt of macOS.
static OSStatus maiSet(const char *service, const char *account, const char *label,
		const void *data, long len) {
	OSStatus st = maiDelete(service, account);
	if (st != errSecSuccess && st != errSecItemNotFound) {
		return st;
	}

	SecTrustedApplicationRef self = NULL;
	st = SecTrustedApplicationCreateFromPath(NULL, &self);
	if (st != errSecSuccess) {
		return st;
	}
	const void *apps[] = { self };
	CFArrayRef trusted = CFArrayCreate(NULL, apps, 1, &kCFTypeArrayCallBacks);
	CFRelease(self);

	CFStringRef desc = maiString(label);
	SecAccessRef access = NULL;
	st = SecAccessCreate(desc, trusted, &access);
	CFRelease(trusted);
	if (st != errSecSuccess) {
		CFRelease(desc);
		return st;
	}

	CFMutableDictionaryRef q = maiQuery(service, account);
	CFDataRef value = CFDataCreate(NULL, (const UInt8 *)data, (CFIndex)len);
	CFDictionarySetValue(q, kSecAttrLabel, desc);
	CFDictionarySetValue(q, kSecValueData, value);
	CFDictionarySetValue(q, kSecAttrAccess, access);
	st = SecItemAdd(q, NULL);
	CFRelease(value);
	CFRelease(access);
	CFRelease(desc);
	CFRelease(q);
	return st;
}

// maiGet copies the secret of the item into out. The caller releases out.
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

// errSecItemNotFound is the status of the Security framework for a missing item.
const errSecItemNotFound = -25300

// System returns the keychain of the OS. On macOS it is the Security framework. Each item
// that it writes has an access list that holds only the mai executable.
func System() Store { return securityStore{} }

// securityStore is a Store on the Security framework of macOS. When noUI is true, a read
// that needs a prompt fails and macOS shows no prompt.
type securityStore struct{ noUI bool }

func (s securityStore) Set(service, account, secret string) error {
	cs, ca := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	cl := C.CString(service + " " + account)
	defer C.free(unsafe.Pointer(cl))

	data := []byte(secret)
	defer clear(data)
	var ptr unsafe.Pointer
	if len(data) > 0 {
		ptr = unsafe.Pointer(&data[0])
	}
	if st := C.maiSet(cs, ca, cl, ptr, C.long(len(data))); st != C.errSecSuccess {
		return statusError("set", st)
	}
	return nil
}

func (s securityStore) Get(service, account string) (string, error) {
	cs, ca := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	noUI := C.int(0)
	if s.noUI {
		noUI = 1
	}
	var out C.CFDataRef
	if st := C.maiGet(cs, ca, noUI, &out); st != C.errSecSuccess {
		if st == errSecItemNotFound {
			return "", ErrNotFound
		}
		return "", statusError("get", st)
	}
	defer C.CFRelease(C.CFTypeRef(out))
	n := C.CFDataGetLength(out)
	if n == 0 {
		return "", nil
	}
	return C.GoStringN((*C.char)(unsafe.Pointer(C.CFDataGetBytePtr(out))), C.int(n)), nil
}

func (s securityStore) Delete(service, account string) error {
	cs, ca := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ca))
	if st := C.maiDelete(cs, ca); st != C.errSecSuccess {
		if st == errSecItemNotFound {
			return ErrNotFound
		}
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

func statusError(op string, st C.OSStatus) error {
	buf := make([]byte, 256)
	C.maiErrorMessage(st, (*C.char)(unsafe.Pointer(&buf[0])), C.long(len(buf)))
	msg := C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
	return &statusErr{op: op, status: int(st), message: msg}
}
