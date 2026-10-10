#pragma once


#include <ApplicationServices/ApplicationServices.h>

enum { kindCmdDown = 1, kindCmdUp = 2, kindOther = 3 };

bool cmdcmdHasAccess(void);
void cmdcmdRequestAccess(void);
bool cmdcmdStartTap(void);
