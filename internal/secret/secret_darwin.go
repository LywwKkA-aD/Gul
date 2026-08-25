//go:build darwin && cgo

package secret

/*
#cgo CFLAGS: -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework CoreFoundation -framework Security

#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

// The keychain is driven through CoreFoundation dictionaries. Building them
// from Go would mean a retain/release dance across the cgo boundary on every
// key; keeping it in C means every CFRelease sits next to the create that
// needs it, which is the project's cgo convention (internal/dsp).
//
// SecItem* is used directly rather than /usr/bin/security: the CLI takes the
// password as an argument, where every process on the machine can read it out
// of the process list.

// gulSecQuery builds the identifying half of an item: the generic-password
// class plus the service/account pair that names it. NULL on allocation
// failure. The caller releases the result.
static CFMutableDictionaryRef gulSecQuery(const char *service, const char *account) {
    CFStringRef svc = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
    CFStringRef acc = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
    CFMutableDictionaryRef query = NULL;
    if (svc != NULL && acc != NULL) {
        query = CFDictionaryCreateMutable(NULL, 4,
            &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    }
    if (query != NULL) {
        // The dictionary retains what it is given, so the local references
        // are released below either way.
        CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
        CFDictionarySetValue(query, kSecAttrService, svc);
        CFDictionarySetValue(query, kSecAttrAccount, acc);
    }
    if (svc != NULL) {
        CFRelease(svc);
    }
    if (acc != NULL) {
        CFRelease(acc);
    }
    return query;
}

// gulSecSet writes the secret, updating an item that already exists.
//
// Update-then-add rather than delete-then-add: SecItemAdd on an existing item
// returns errSecDuplicateItem, and deleting first would leave the account with
// no password at all if the process died in between.
static OSStatus gulSecSet(const char *service, const char *account,
                          const void *secret, int secretLen) {
    CFMutableDictionaryRef query = gulSecQuery(service, account);
    if (query == NULL) {
        return errSecAllocate;
    }
    CFDataRef data = CFDataCreate(NULL, (const UInt8 *)secret, (CFIndex)secretLen);
    if (data == NULL) {
        CFRelease(query);
        return errSecAllocate;
    }
    CFMutableDictionaryRef update = CFDictionaryCreateMutable(NULL, 1,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    if (update == NULL) {
        CFRelease(data);
        CFRelease(query);
        return errSecAllocate;
    }
    CFDictionarySetValue(update, kSecValueData, data);

    OSStatus status = SecItemUpdate(query, update);
    if (status == errSecItemNotFound) {
        CFDictionarySetValue(query, kSecValueData, data);
        status = SecItemAdd(query, NULL);
    }

    CFRelease(update);
    CFRelease(data);
    CFRelease(query);
    return status;
}

// gulSecGet copies the secret into a malloc'd buffer the caller frees.
// *out is NULL and *outLen 0 unless errSecSuccess is returned.
static OSStatus gulSecGet(const char *service, const char *account,
                          void **out, int *outLen) {
    *out = NULL;
    *outLen = 0;

    CFMutableDictionaryRef query = gulSecQuery(service, account);
    if (query == NULL) {
        return errSecAllocate;
    }
    CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
    CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);

    CFDataRef data = NULL;
    OSStatus status = SecItemCopyMatching(query, (CFTypeRef *)&data);
    CFRelease(query);
    if (status != errSecSuccess) {
        if (data != NULL) {
            CFRelease(data);
        }
        return status;
    }
    if (data == NULL) {
        return errSecItemNotFound;
    }

    CFIndex length = CFDataGetLength(data);
    if (length > 0) {
        void *buf = malloc((size_t)length);
        if (buf == NULL) {
            CFRelease(data);
            return errSecAllocate;
        }
        memcpy(buf, CFDataGetBytePtr(data), (size_t)length);
        *out = buf;
        *outLen = (int)length;
    }
    CFRelease(data);
    return errSecSuccess;
}

// gulSecDelete removes the item. errSecItemNotFound means it was not there.
static OSStatus gulSecDelete(const char *service, const char *account) {
    CFMutableDictionaryRef query = gulSecQuery(service, account);
    if (query == NULL) {
        return errSecAllocate;
    }
    OSStatus status = SecItemDelete(query);
    CFRelease(query);
    return status;
}

// gulSecProbe asks whether the keychain answers at all, without reserving an
// account name for the purpose and without returning any data. A working
// keychain replies errSecSuccess (this service has an item) or
// errSecItemNotFound (it has none); anything else is the store being unusable.
static OSStatus gulSecProbe(const char *service) {
    CFStringRef svc = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
    if (svc == NULL) {
        return errSecAllocate;
    }
    CFMutableDictionaryRef query = CFDictionaryCreateMutable(NULL, 3,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    if (query == NULL) {
        CFRelease(svc);
        return errSecAllocate;
    }
    CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
    CFDictionarySetValue(query, kSecAttrService, svc);
    CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
    CFRelease(svc);

    OSStatus status = SecItemCopyMatching(query, NULL);
    CFRelease(query);
    return status;
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// darwinStore is the macOS keychain, one generic-password item per account
// under a single service name.
//
// The SecItem API is thread safe and holds no state of ours, so the store is
// just the service name plus the cached answer to Available.
type darwinStore struct {
	service string

	probeOnce sync.Once
	probeOK   bool
}

func newStore(service string) Store { return &darwinStore{service: service} }

func (s *darwinStore) Set(account, value string) error {
	key, err := accountKey(account)
	if err != nil {
		return err
	}
	if err := checkSecret(value); err != nil {
		return err
	}

	cService, cAccount, free := s.cStrings(key)
	defer free()

	blob := []byte(value)
	status := C.gulSecSet(cService, cAccount, unsafe.Pointer(&blob[0]), C.int(len(blob)))
	return statusError("write", status)
}

func (s *darwinStore) Get(account string) (string, bool, error) {
	key, err := accountKey(account)
	if err != nil {
		return "", false, err
	}

	cService, cAccount, free := s.cStrings(key)
	defer free()

	var (
		out    unsafe.Pointer
		outLen C.int
	)
	status := C.gulSecGet(cService, cAccount, &out, &outLen)
	if status == C.errSecItemNotFound {
		return "", false, nil
	}
	if err := statusError("read", status); err != nil {
		return "", false, err
	}
	// free(NULL) is a no-op, so this covers the empty case below as well.
	defer C.free(out)
	if out == nil || outLen <= 0 {
		// An item with an empty payload is not a password. Nothing this
		// package writes can produce one (Set refuses an empty secret), so
		// it can only have come from elsewhere.
		return "", false, nil
	}
	return C.GoStringN((*C.char)(out), outLen), true, nil
}

func (s *darwinStore) Delete(account string) error {
	key, err := accountKey(account)
	if err != nil {
		return err
	}

	cService, cAccount, free := s.cStrings(key)
	defer free()

	status := C.gulSecDelete(cService, cAccount)
	if status == C.errSecItemNotFound {
		// Deleting what is not there is the state the caller asked for.
		return nil
	}
	return statusError("delete", status)
}

// Available probes once. The keychain is a property of the login session, and
// a session that starts without one does not grow one while the client runs;
// caching keeps a Servers() call from paying for a probe per entry.
func (s *darwinStore) Available() bool {
	s.probeOnce.Do(func() {
		cService := C.CString(s.service)
		defer C.free(unsafe.Pointer(cService))
		status := C.gulSecProbe(cService)
		s.probeOK = status == C.errSecSuccess || status == C.errSecItemNotFound
	})
	return s.probeOK
}

// cStrings converts the pair every call needs and returns the one function
// that releases both.
func (s *darwinStore) cStrings(key string) (service, account *C.char, free func()) {
	service = C.CString(s.service)
	account = C.CString(key)
	return service, account, func() {
		C.free(unsafe.Pointer(service))
		C.free(unsafe.Pointer(account))
	}
}

// statusError maps an OSStatus onto the package vocabulary. The statuses that
// mean "this machine has no usable keychain right now" wrap ErrUnavailable so
// the caller can degrade instead of failing; everything else is a real fault
// and carries the numeric status, which is what Apple's documentation and
// Console messages are indexed by.
func statusError(op string, status C.OSStatus) error {
	if status == C.errSecSuccess {
		return nil
	}
	if unusableStatus(status) {
		return fmt.Errorf("%w: keychain %s: OSStatus %d", ErrUnavailable, op, int(status))
	}
	return fmt.Errorf("keychain %s: OSStatus %d", op, int(status))
}

func unusableStatus(status C.OSStatus) bool {
	switch status {
	case C.errSecNotAvailable, C.errSecInteractionNotAllowed, C.errSecMissingEntitlement:
		// No keychain is available, the session may not prompt for one, or
		// this build is not entitled to reach it. All three are "no store",
		// not "the password is wrong".
		return true
	}
	return false
}
