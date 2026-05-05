# Add project specific ProGuard rules here.
-keepattributes Signature
-keepattributes *Annotation*

# Retrofit
-keep class retrofit2.** { *; }
-dontwarn retrofit2.**

# Gson
-keep class com.google.gson.** { *; }
-keep class com.screwy.igloo.data.remote.dto.** { *; }

# Room
-keep class * extends androidx.room.RoomDatabase { *; }
