// odhelper: creates / removes the Open Directory LDAPv3 node for cs-team (LDAPS, RFC2307 mappings).
// Run as root. Compiled universal (x86_64 + arm64) and embedded into cs-ldapclient (see build.sh).
//
//   odhelper add <host> <base> [port]
//   odhelper remove <host>
//
// What macOS 12 needs (found in the live test, see docs/TODO_cs-ldapclient.md):
//  - packetEncryption = 3 (SSL) and explicit timeouts: with timeouts 0 opendirectoryd fails with "rootDSE error -9"
//  - full record mappings (the template name alone gives "no mapping"), static home /Users/$uid$ and shell /bin/zsh
//  - module option "Template Search Base Suffix" = base
//  - no search account: cs-team must run with CS_LDAP_ANON=1 (the node binds anonymously)
#import <Foundation/Foundation.h>
#import <OpenDirectory/OpenDirectory.h>
#import <SecurityFoundation/SFAuthorization.h>

static ODRecordMap *rm(NSDictionary *tpl, NSString *stdType, NSString *cls, NSString *home, NSString *shell) {
    NSDictionary *at = tpl[@"recordtypes"][stdType][@"attributetypes"];
    ODRecordMap *m = [ODRecordMap recordMap];
    m.odPredicate = @{@"operator": @"OR", @"predicates": @[@{@"attribute": @"objectClass", @"values": @[cls], @"matchtype": @(0x2001)}]};
    for (NSString *k in at) {
        NSString *nat = at[k][@"native"];
        if (!nat) continue;
        ODAttributeMap *a;
        if (home && [k isEqualToString:@"dsAttrTypeStandard:NFSHomeDirectory"]) {
            a = [ODAttributeMap attributeMapWithValue:nat];
            [a setVariableSubstitution:home];
        } else if (shell && [k isEqualToString:@"dsAttrTypeStandard:UserShell"]) {
            a = [ODAttributeMap attributeMapWithStaticValue:shell];
        } else {
            a = [ODAttributeMap attributeMapWithValue:nat];
        }
        [m setAttributeMap:a forStandardAttribute:k];
    }
    return m;
}

int main(int argc, char **argv) {
    @autoreleasepool {
        if (argc < 3) { fprintf(stderr, "usage: odhelper add <host> <base> [port] | remove <host>\n"); return 2; }
        NSString *cmd = [NSString stringWithUTF8String:argv[1]];
        NSString *host = [NSString stringWithUTF8String:argv[2]];
        NSString *node = [@"/LDAPv3/" stringByAppendingString:host];
        NSError *e = nil;
        SFAuthorization *auth = [SFAuthorization authorization];
        if ([cmd isEqualToString:@"remove"]) {
            ODSession *s = [ODSession defaultSession];
            BOOL ok = [s deleteConfigurationWithNodename:node authorization:auth error:&e];
            if (!ok) { fprintf(stderr, "remove: %s\n", [[e description] UTF8String]); return 1; }
            printf("removed %s\n", [node UTF8String]);
            return 0;
        }
        if (![cmd isEqualToString:@"add"] || argc < 4) { fprintf(stderr, "usage\n"); return 2; }
        NSString *base = [NSString stringWithUTF8String:argv[3]];
        int port = argc > 4 ? atoi(argv[4]) : 636;
        NSDictionary *tpl = [NSDictionary dictionaryWithContentsOfFile:@"/System/Library/OpenDirectory/Mappings/RFC2307.plist"];
        if (!tpl) { fprintf(stderr, "RFC2307 mapping template not found\n"); return 1; }
        ODModuleEntry *me = [ODModuleEntry moduleEntryWithName:@"ldap" xpcServiceName:nil];
        [me setOption:@"Template Search Base Suffix" value:base];
        [me setOption:@"Denied SASL Methods" value:@[@"DIGEST-MD5"]];
        ODConfiguration *c = [ODConfiguration configuration];
        c.nodeName = node;
        c.templateName = @"LDAPv3";
        c.comment = @"cs-team";
        c.preferredDestinationHostName = host;
        c.preferredDestinationHostPort = port;
        c.packetEncryption = 3;
        c.queryTimeoutInSeconds = 30;
        c.connectionSetupTimeoutInSeconds = 15;
        c.connectionIdleTimeoutInSeconds = 120;
        ODMappings *m = [ODMappings mappings];
        m.comment = @"cs-team RFC2307";
        [m setRecordMap:rm(tpl, @"dsRecTypeStandard:Users", @"posixAccount", @"/Users/$uid$", @"/bin/zsh") forStandardRecordType:@"dsRecTypeStandard:Users"];
        [m setRecordMap:rm(tpl, @"dsRecTypeStandard:Groups", @"posixGroup", nil, nil) forStandardRecordType:@"dsRecTypeStandard:Groups"];
        c.defaultMappings = m;
        c.defaultModuleEntries = @[me];
        BOOL ok = [c saveUsingAuthorization:auth error:&e];
        if (!ok) { fprintf(stderr, "save: %s\n", [[e description] UTF8String]); return 1; }
        printf("saved %s\n", [node UTF8String]);
    }
    return 0;
}
