using System;
using System.Linq;
using UnityEditor;
using UnityEditor.Build.Reporting;
#if UNITY_6000_0_OR_NEWER
using UnityEditor.Build.Profile;
#endif
using UnityEngine;

namespace SteamReleaser.Editor
{
    /// <summary>
    /// Optional build entry point for Unity projects that need a custom method.
    /// Copy this file into Assets/Editor and configure:
    /// execute_method: SteamReleaser.Editor.BuildCommand.Build
    ///
    /// Steam Releaser starts Unity with -buildTarget or -activeBuildProfile
    /// before invoking this method, so platform-dependent compilation is correct.
    /// </summary>
    public static class BuildCommand
    {
        public static void Build()
        {
            var output = Argument("-steamReleaserOutput");
            if (string.IsNullOrWhiteSpace(output))
            {
                throw new Exception("Steam Releaser did not provide -steamReleaserOutput.");
            }

            BuildReport report;
#if UNITY_6000_0_OR_NEWER
            if (!string.IsNullOrEmpty(Argument("-activeBuildProfile")))
            {
                var profile = BuildProfile.GetActiveBuildProfile();
                if (profile == null)
                {
                    throw new Exception("Unity did not activate the requested Build Profile.");
                }

                report = BuildPipeline.BuildPlayer(new BuildPlayerWithProfileOptions
                {
                    buildProfile = profile,
                    locationPathName = output,
                    options = BuildOptions.None,
                });
            }
            else
#endif
            {
                var scenes = EditorBuildSettings.scenes
                    .Where(scene => scene.enabled)
                    .Select(scene => scene.path)
                    .ToArray();
                if (scenes.Length == 0)
                {
                    throw new Exception("No enabled scenes are configured in Build Settings.");
                }

                report = BuildPipeline.BuildPlayer(new BuildPlayerOptions
                {
                    scenes = scenes,
                    locationPathName = output,
                    target = EditorUserBuildSettings.activeBuildTarget,
                    options = BuildOptions.None,
                });
            }

            if (report.summary.result != BuildResult.Succeeded)
            {
                throw new Exception($"Unity build failed: {report.summary.result}");
            }

            Debug.Log($"Steam Releaser build completed: {output} ({report.summary.totalSize} bytes)");
        }

        private static string Argument(string name)
        {
            var args = Environment.GetCommandLineArgs();
            for (var index = 0; index < args.Length - 1; index++)
            {
                if (string.Equals(args[index], name, StringComparison.Ordinal))
                {
                    return args[index + 1];
                }
            }
            return string.Empty;
        }
    }
}
